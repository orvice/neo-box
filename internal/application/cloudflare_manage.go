package application

import (
	"context"
	"sort"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	"go.orx.me/apps/neo-box/internal/cloudflareacct"
	cfrepo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

// --- Workers and Pages management ---

const defaultDeploymentListSize = 25

// change opens the caller's connection and runs one logged change on it.
func (s *CloudflareServiceServer) change(
	ctx context.Context, connectionID string,
	run func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error),
) (*neoboxv1.CloudflareChangeResult, error) {
	d, a, err := s.openAccount(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	res, err := run(a, d.user)
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	return changeResultToProto(res), nil
}

func changeResultToProto(r *cloudflareacct.OperationResult) *neoboxv1.CloudflareChangeResult {
	out := &neoboxv1.CloudflareChangeResult{}
	if r == nil || r.Operation == nil {
		return out
	}
	out.Changed = true
	out.Operation = resourceOperationToProto(r.Operation)
	if r.LogErr != nil {
		out.OperationLogError = "the change was made, but its outcome could not be added to the operation log, which shows it as pending: " + r.LogErr.Error()
	}
	return out
}

func resourceOperationToProto(op *cfrepo.ResourceOperation) *neoboxv1.CloudflareResourceOperation {
	out := &neoboxv1.CloudflareResourceOperation{
		Id: op.ID, ConnectionId: op.ConnectionID, AccountId: op.AccountID,
		Kind: resourceKindToProto(op.Kind), Resource: op.Resource, Action: op.Action, Target: op.Target,
		Status: dnsStatusToProto(op.Status), Error: op.Error,
		ActorId: op.ActorID, ActorName: op.ActorName, CreatedAt: timestamppb.New(op.CreatedAt),
	}
	if !op.FinishedAt.IsZero() {
		out.FinishedAt = timestamppb.New(op.FinishedAt)
	}
	// A body that doesn't fit a Struct is left out rather than failing the
	// whole list.
	for _, f := range []struct {
		in  map[string]any
		out **structpb.Struct
	}{{op.Before, &out.Before}, {op.After, &out.After}, {op.Requested, &out.Requested}} {
		if f.in == nil {
			continue
		}
		if st, err := structpb.NewStruct(f.in); err == nil {
			*f.out = st
		}
	}
	return out
}

func resourceKindToProto(k cfrepo.ResourceKind) neoboxv1.CloudflareResourceKind {
	switch k {
	case cfrepo.KindWorker:
		return neoboxv1.CloudflareResourceKind_CLOUDFLARE_RESOURCE_KIND_WORKER
	case cfrepo.KindPages:
		return neoboxv1.CloudflareResourceKind_CLOUDFLARE_RESOURCE_KIND_PAGES
	case cfrepo.KindEmailRouting:
		return neoboxv1.CloudflareResourceKind_CLOUDFLARE_RESOURCE_KIND_EMAIL_ROUTING
	}
	return neoboxv1.CloudflareResourceKind_CLOUDFLARE_RESOURCE_KIND_UNSPECIFIED
}

func (s *CloudflareServiceServer) ListCloudflareResourceOperations(ctx context.Context, req *connect.Request[neoboxv1.ListCloudflareResourceOperationsRequest]) (*connect.Response[neoboxv1.ListCloudflareResourceOperationsResponse], error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	c, err := d.load(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	var kind cfrepo.ResourceKind
	switch req.Msg.GetKind() {
	case neoboxv1.CloudflareResourceKind_CLOUDFLARE_RESOURCE_KIND_WORKER:
		kind = cfrepo.KindWorker
	case neoboxv1.CloudflareResourceKind_CLOUDFLARE_RESOURCE_KIND_PAGES:
		kind = cfrepo.KindPages
	case neoboxv1.CloudflareResourceKind_CLOUDFLARE_RESOURCE_KIND_EMAIL_ROUTING:
		kind = cfrepo.KindEmailRouting
	}
	page, size := pageArgs(req.Msg.GetPage(), req.Msg.GetPageSize(), defaultOperationsSize, 1, maxOperationsSize)
	ops, total, err := d.repo.ListResourceOperations(ctx, cfrepo.ResourceFilter{
		UserID: d.user.ID, ConnectionID: c.ID, Kind: kind, Resource: strings.TrimSpace(req.Msg.GetResource()),
		Offset: (page - 1) * size, Limit: size,
	})
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	out := make([]*neoboxv1.CloudflareResourceOperation, 0, len(ops))
	for _, op := range ops {
		out = append(out, resourceOperationToProto(op))
	}
	return connect.NewResponse(&neoboxv1.ListCloudflareResourceOperationsResponse{Operations: out, PageInfo: pageInfoOf(page, size, total)}), nil
}

// --- Workers ---

func (s *CloudflareServiceServer) GetCloudflareWorker(ctx context.Context, req *connect.Request[neoboxv1.GetCloudflareWorkerRequest]) (*connect.Response[neoboxv1.GetCloudflareWorkerResponse], error) {
	_, a, err := s.openAccount(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	w, err := a.Worker(ctx, strings.TrimSpace(req.Msg.GetName()))
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	out := &neoboxv1.GetCloudflareWorkerResponse{
		Name: w.Name, WorkersDevSubdomain: w.Subdomain, Issues: readIssuesToProto(w.Issues),
	}
	if !w.CreatedOn.IsZero() {
		out.CreatedAt = timestamppb.New(w.CreatedOn)
	}
	if !w.ModifiedOn.IsZero() {
		out.ModifiedAt = timestamppb.New(w.ModifiedOn)
	}
	if w.WorkersDev != nil {
		out.WorkersDevEnabled = &w.WorkersDev.Enabled
	}
	for _, d := range w.Domains {
		out.Domains = append(out.Domains, &neoboxv1.CloudflareWorkerDomain{
			Hostname: d.Hostname, ZoneId: d.ZoneID, ZoneName: d.ZoneName, Enabled: d.Enabled == nil || *d.Enabled,
		})
	}
	for _, r := range w.Routes {
		out.Routes = append(out.Routes, &neoboxv1.CloudflareWorkerRoute{Id: r.ID, Pattern: r.Pattern, ZoneId: r.ZoneID, ZoneName: r.ZoneName})
	}
	for _, c := range w.Schedules {
		out.Crons = append(out.Crons, c.Cron)
	}
	for _, sec := range w.Secrets {
		out.Secrets = append(out.Secrets, &neoboxv1.CloudflareWorkerSecret{Name: sec.Name, Type: sec.Type})
	}
	if st := w.Settings; st != nil {
		out.CompatibilityDate, out.CompatibilityFlags = st.CompatibilityDate, st.CompatibilityFlags
		for _, b := range st.Bindings {
			out.Bindings = append(out.Bindings, &neoboxv1.CloudflareWorkerBinding{Name: b.Name, Type: b.Type})
		}
	}
	for _, d := range w.Deployments {
		pd := &neoboxv1.CloudflareWorkerDeployment{
			Id: d.ID, Source: d.Source, Message: d.Message(), AuthorEmail: d.AuthorEmail,
		}
		if !d.CreatedOn.IsZero() {
			pd.CreatedAt = timestamppb.New(d.CreatedOn)
		}
		for _, v := range d.Versions {
			pd.Versions = append(pd.Versions, &neoboxv1.CloudflareWorkerDeploymentVersion{VersionId: v.VersionID, Percentage: v.Percentage})
		}
		out.Deployments = append(out.Deployments, pd)
	}
	for _, v := range w.Versions {
		pv := &neoboxv1.CloudflareWorkerVersion{Id: v.ID, Number: int32(v.Number), Source: v.Metadata.Source, AuthorEmail: v.Metadata.AuthorEmail}
		if !v.Metadata.CreatedOn.IsZero() {
			pv.CreatedAt = timestamppb.New(v.Metadata.CreatedOn)
		}
		out.Versions = append(out.Versions, pv)
	}
	return connect.NewResponse(out), nil
}

func trim(s string) string { return strings.TrimSpace(s) }

func (s *CloudflareServiceServer) SetCloudflareWorkerWorkersDev(ctx context.Context, req *connect.Request[neoboxv1.SetCloudflareWorkerWorkersDevRequest]) (*connect.Response[neoboxv1.SetCloudflareWorkerWorkersDevResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.SetWorkersDev(ctx, actor, trim(req.Msg.GetName()), req.Msg.GetEnabled())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.SetCloudflareWorkerWorkersDevResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) AttachCloudflareWorkerDomain(ctx context.Context, req *connect.Request[neoboxv1.AttachCloudflareWorkerDomainRequest]) (*connect.Response[neoboxv1.AttachCloudflareWorkerDomainResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.AttachWorkerDomain(ctx, actor, trim(req.Msg.GetName()), req.Msg.GetHostname())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.AttachCloudflareWorkerDomainResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) DetachCloudflareWorkerDomain(ctx context.Context, req *connect.Request[neoboxv1.DetachCloudflareWorkerDomainRequest]) (*connect.Response[neoboxv1.DetachCloudflareWorkerDomainResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.DetachWorkerDomain(ctx, actor, trim(req.Msg.GetName()), req.Msg.GetHostname())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.DetachCloudflareWorkerDomainResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) AddCloudflareWorkerRoute(ctx context.Context, req *connect.Request[neoboxv1.AddCloudflareWorkerRouteRequest]) (*connect.Response[neoboxv1.AddCloudflareWorkerRouteResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.AddWorkerRoute(ctx, actor, trim(req.Msg.GetName()), req.Msg.GetPattern())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.AddCloudflareWorkerRouteResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) DeleteCloudflareWorkerRoute(ctx context.Context, req *connect.Request[neoboxv1.DeleteCloudflareWorkerRouteRequest]) (*connect.Response[neoboxv1.DeleteCloudflareWorkerRouteResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.DeleteWorkerRoute(ctx, actor, trim(req.Msg.GetName()), trim(req.Msg.GetZoneId()), trim(req.Msg.GetRouteId()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.DeleteCloudflareWorkerRouteResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) AddCloudflareWorkerCron(ctx context.Context, req *connect.Request[neoboxv1.AddCloudflareWorkerCronRequest]) (*connect.Response[neoboxv1.AddCloudflareWorkerCronResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.AddWorkerSchedule(ctx, actor, trim(req.Msg.GetName()), req.Msg.GetCron())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.AddCloudflareWorkerCronResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) DeleteCloudflareWorkerCron(ctx context.Context, req *connect.Request[neoboxv1.DeleteCloudflareWorkerCronRequest]) (*connect.Response[neoboxv1.DeleteCloudflareWorkerCronResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.DeleteWorkerSchedule(ctx, actor, trim(req.Msg.GetName()), req.Msg.GetCron())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.DeleteCloudflareWorkerCronResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) PutCloudflareWorkerSecret(ctx context.Context, req *connect.Request[neoboxv1.PutCloudflareWorkerSecretRequest]) (*connect.Response[neoboxv1.PutCloudflareWorkerSecretResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.PutWorkerSecret(ctx, actor, trim(req.Msg.GetName()), trim(req.Msg.GetSecretName()), req.Msg.GetValue())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.PutCloudflareWorkerSecretResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) DeleteCloudflareWorkerSecret(ctx context.Context, req *connect.Request[neoboxv1.DeleteCloudflareWorkerSecretRequest]) (*connect.Response[neoboxv1.DeleteCloudflareWorkerSecretResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.DeleteWorkerSecret(ctx, actor, trim(req.Msg.GetName()), trim(req.Msg.GetSecretName()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.DeleteCloudflareWorkerSecretResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) RollbackCloudflareWorker(ctx context.Context, req *connect.Request[neoboxv1.RollbackCloudflareWorkerRequest]) (*connect.Response[neoboxv1.RollbackCloudflareWorkerResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.RollbackWorker(ctx, actor, trim(req.Msg.GetName()), req.Msg.GetVersionId())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.RollbackCloudflareWorkerResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) DeleteCloudflareWorker(ctx context.Context, req *connect.Request[neoboxv1.DeleteCloudflareWorkerRequest]) (*connect.Response[neoboxv1.DeleteCloudflareWorkerResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.DeleteWorker(ctx, actor, trim(req.Msg.GetName()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.DeleteCloudflareWorkerResponse{Result: r}), nil
}

// --- Pages ---

func pagesEnvToProto(e string) neoboxv1.CloudflarePagesEnvironment {
	switch e {
	case cloudflareacct.PagesProduction:
		return neoboxv1.CloudflarePagesEnvironment_CLOUDFLARE_PAGES_ENVIRONMENT_PRODUCTION
	case cloudflareacct.PagesPreview:
		return neoboxv1.CloudflarePagesEnvironment_CLOUDFLARE_PAGES_ENVIRONMENT_PREVIEW
	}
	return neoboxv1.CloudflarePagesEnvironment_CLOUDFLARE_PAGES_ENVIRONMENT_UNSPECIFIED
}

// pagesEnvFromProto turns the enum into the environment name, "" for
// unspecified and an InvalidArgument for a value that does not exist.
func pagesEnvFromProto(e neoboxv1.CloudflarePagesEnvironment) (string, error) {
	switch e {
	case neoboxv1.CloudflarePagesEnvironment_CLOUDFLARE_PAGES_ENVIRONMENT_UNSPECIFIED:
		return "", nil
	case neoboxv1.CloudflarePagesEnvironment_CLOUDFLARE_PAGES_ENVIRONMENT_PRODUCTION:
		return cloudflareacct.PagesProduction, nil
	case neoboxv1.CloudflarePagesEnvironment_CLOUDFLARE_PAGES_ENVIRONMENT_PREVIEW:
		return cloudflareacct.PagesPreview, nil
	}
	return "", connectx.InvalidArgument("environment", "must be production or preview")
}

// requiredPagesEnv is pagesEnvFromProto for requests that need one.
func requiredPagesEnv(e neoboxv1.CloudflarePagesEnvironment) (string, error) {
	env, err := pagesEnvFromProto(e)
	if err == nil && env == "" {
		err = connectx.InvalidArgument("environment", "is required")
	}
	return env, err
}

func envVarsToProto(vars map[string]*cloudflare.PagesEnvVar) []*neoboxv1.CloudflarePagesEnvVar {
	names := make([]string, 0, len(vars))
	for n := range vars {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*neoboxv1.CloudflarePagesEnvVar, 0, len(names))
	for _, n := range names {
		v := vars[n]
		pv := &neoboxv1.CloudflarePagesEnvVar{Name: n, Type: neoboxv1.CloudflarePagesEnvVarType_CLOUDFLARE_PAGES_ENV_VAR_TYPE_SECRET}
		if v.Type == cloudflare.PagesEnvPlainText {
			pv.Type, pv.Value = neoboxv1.CloudflarePagesEnvVarType_CLOUDFLARE_PAGES_ENV_VAR_TYPE_PLAIN_TEXT, v.Value
		}
		out = append(out, pv)
	}
	return out
}

func (s *CloudflareServiceServer) GetCloudflarePagesProject(ctx context.Context, req *connect.Request[neoboxv1.GetCloudflarePagesProjectRequest]) (*connect.Response[neoboxv1.GetCloudflarePagesProjectResponse], error) {
	_, a, err := s.openAccount(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	d, err := a.PagesProjectDetail(ctx, trim(req.Msg.GetName()))
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	p := d.Project
	out := &neoboxv1.GetCloudflarePagesProjectResponse{
		Name: p.Name, Subdomain: p.Subdomain, ProductionBranch: p.ProductionBranch,
		BuildConfig: &neoboxv1.CloudflarePagesBuildConfig{
			BuildCommand: p.BuildConfig.BuildCommand, DestinationDir: p.BuildConfig.DestinationDir, RootDir: p.BuildConfig.RootDir,
			BuildCaching: p.BuildConfig.BuildCaching != nil && *p.BuildConfig.BuildCaching,
		},
		ProductionEnvVars: envVarsToProto(p.DeploymentConfigs.Production.EnvVars),
		PreviewEnvVars:    envVarsToProto(p.DeploymentConfigs.Preview.EnvVars),
		Issues:            readIssuesToProto(d.Issues),
	}
	if !p.CreatedOn.IsZero() {
		out.CreatedAt = timestamppb.New(p.CreatedOn)
	}
	if p.Source != nil {
		out.GitProvider = p.Source.Type
		out.GitRepository = strings.Trim(p.Source.Config.Owner+"/"+p.Source.Config.RepoName, "/")
	}
	for _, dom := range d.Domains {
		out.Domains = append(out.Domains, &neoboxv1.CloudflarePagesDomain{Name: dom.Name, Status: dom.Status})
	}
	return connect.NewResponse(out), nil
}

func deploymentToProto(d *cloudflare.PagesDeployment) *neoboxv1.CloudflarePagesDeployment {
	out := &neoboxv1.CloudflarePagesDeployment{
		Id: d.ID, ShortId: d.ShortID, Url: d.URL, Environment: pagesEnvToProto(d.Environment),
		Branch: d.Trigger.Metadata.Branch, CommitHash: d.Trigger.Metadata.CommitHash, CommitMessage: d.Trigger.Metadata.CommitMessage,
		Stage: d.LatestStage.Name, StageStatus: d.LatestStage.Status, IsSkipped: d.IsSkipped, Aliases: d.Aliases,
	}
	if !d.CreatedOn.IsZero() {
		out.CreatedAt = timestamppb.New(d.CreatedOn)
	}
	return out
}

func (s *CloudflareServiceServer) ListCloudflarePagesDeployments(ctx context.Context, req *connect.Request[neoboxv1.ListCloudflarePagesDeploymentsRequest]) (*connect.Response[neoboxv1.ListCloudflarePagesDeploymentsResponse], error) {
	env, err := pagesEnvFromProto(req.Msg.GetEnvironment())
	if err != nil {
		return nil, err
	}
	_, a, err := s.openAccount(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	page, size := pageArgs(req.Msg.GetPage(), req.Msg.GetPageSize(), defaultDeploymentListSize, 1, maxOperationsSize)
	list, info, err := a.PagesDeployments(ctx, trim(req.Msg.GetName()), env, page, size)
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	out := &neoboxv1.ListCloudflarePagesDeploymentsResponse{PageInfo: cfPageInfoToProto(info, page, size)}
	for i := range list {
		out.Deployments = append(out.Deployments, deploymentToProto(&list[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *CloudflareServiceServer) CreateCloudflarePagesProject(ctx context.Context, req *connect.Request[neoboxv1.CreateCloudflarePagesProjectRequest]) (*connect.Response[neoboxv1.CreateCloudflarePagesProjectResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.CreatePagesProject(ctx, actor, req.Msg.GetName(), req.Msg.GetProductionBranch())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.CreateCloudflarePagesProjectResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) UpdateCloudflarePagesProject(ctx context.Context, req *connect.Request[neoboxv1.UpdateCloudflarePagesProjectRequest]) (*connect.Response[neoboxv1.UpdateCloudflarePagesProjectResponse], error) {
	m := req.Msg
	r, err := s.change(ctx, m.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.UpdatePagesProject(ctx, actor, trim(m.GetName()), cloudflareacct.PagesProjectUpdate{
			ProductionBranch: m.ProductionBranch, BuildCommand: m.BuildCommand, DestinationDir: m.DestinationDir, RootDir: m.RootDir,
		})
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.UpdateCloudflarePagesProjectResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) DeleteCloudflarePagesProject(ctx context.Context, req *connect.Request[neoboxv1.DeleteCloudflarePagesProjectRequest]) (*connect.Response[neoboxv1.DeleteCloudflarePagesProjectResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.DeletePagesProject(ctx, actor, trim(req.Msg.GetName()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.DeleteCloudflarePagesProjectResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) RetryCloudflarePagesDeployment(ctx context.Context, req *connect.Request[neoboxv1.RetryCloudflarePagesDeploymentRequest]) (*connect.Response[neoboxv1.RetryCloudflarePagesDeploymentResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.RetryPagesDeployment(ctx, actor, trim(req.Msg.GetName()), trim(req.Msg.GetDeploymentId()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.RetryCloudflarePagesDeploymentResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) RollbackCloudflarePagesDeployment(ctx context.Context, req *connect.Request[neoboxv1.RollbackCloudflarePagesDeploymentRequest]) (*connect.Response[neoboxv1.RollbackCloudflarePagesDeploymentResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.RollbackPagesDeployment(ctx, actor, trim(req.Msg.GetName()), trim(req.Msg.GetDeploymentId()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.RollbackCloudflarePagesDeploymentResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) DeleteCloudflarePagesDeployment(ctx context.Context, req *connect.Request[neoboxv1.DeleteCloudflarePagesDeploymentRequest]) (*connect.Response[neoboxv1.DeleteCloudflarePagesDeploymentResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.DeletePagesDeployment(ctx, actor, trim(req.Msg.GetName()), trim(req.Msg.GetDeploymentId()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.DeleteCloudflarePagesDeploymentResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) AddCloudflarePagesDomain(ctx context.Context, req *connect.Request[neoboxv1.AddCloudflarePagesDomainRequest]) (*connect.Response[neoboxv1.AddCloudflarePagesDomainResponse], error) {
	d, a, err := s.openAccount(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	res, err := a.AddPagesDomain(ctx, d.user, trim(req.Msg.GetName()), req.Msg.GetHostname(), req.Msg.GetCreateDns())
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	dns := cloudflareacct.RecordResult{Status: cloudflareacct.RecordNotRequested}
	if res.DNS != nil {
		dns = *res.DNS
	}
	return connect.NewResponse(&neoboxv1.AddCloudflarePagesDomainResponse{
		Result: changeResultToProto(res.OperationResult), Record: recordResultToProto(dns),
	}), nil
}

func (s *CloudflareServiceServer) DeleteCloudflarePagesDomain(ctx context.Context, req *connect.Request[neoboxv1.DeleteCloudflarePagesDomainRequest]) (*connect.Response[neoboxv1.DeleteCloudflarePagesDomainResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.DeletePagesDomain(ctx, actor, trim(req.Msg.GetName()), req.Msg.GetHostname())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.DeleteCloudflarePagesDomainResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) SetCloudflarePagesEnvVar(ctx context.Context, req *connect.Request[neoboxv1.SetCloudflarePagesEnvVarRequest]) (*connect.Response[neoboxv1.SetCloudflarePagesEnvVarResponse], error) {
	env, err := requiredPagesEnv(req.Msg.GetEnvironment())
	if err != nil {
		return nil, err
	}
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.SetPagesEnvVar(ctx, actor, trim(req.Msg.GetName()), env, trim(req.Msg.GetVarName()), req.Msg.GetValue(), req.Msg.GetSecret())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.SetCloudflarePagesEnvVarResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) DeleteCloudflarePagesEnvVar(ctx context.Context, req *connect.Request[neoboxv1.DeleteCloudflarePagesEnvVarRequest]) (*connect.Response[neoboxv1.DeleteCloudflarePagesEnvVarResponse], error) {
	env, err := requiredPagesEnv(req.Msg.GetEnvironment())
	if err != nil {
		return nil, err
	}
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.DeletePagesEnvVar(ctx, actor, trim(req.Msg.GetName()), env, trim(req.Msg.GetVarName()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.DeleteCloudflarePagesEnvVarResponse{Result: r}), nil
}
