package application

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// mailAPI is one user's Gmail mail, read by impersonating that user with
// gmail.readonly. It has no method that changes a mailbox. See docs/adr/0010.
type mailAPI interface {
	ListLabels(context.Context) (*googleworkspace.MailLabels, error)
	SearchMessages(context.Context, googleworkspace.MailSearch) (*googleworkspace.MailPage, error)
	ReadMessage(context.Context, string) (*googleworkspace.MailMessage, error)
	DownloadAttachment(context.Context, string, string) (*googleworkspace.MailAttachmentFile, error)
	SetWriteRecorder(googleworkspace.WriteRecorder)
}

type mailClientFactory func(ctx context.Context, cfg googleworkspace.ConnectionConfig, sec googleworkspace.ConnectionSecret, customer, userEmail, reason string) (mailAPI, error)

func defaultMailClient(ctx context.Context, cfg googleworkspace.ConnectionConfig, sec googleworkspace.ConnectionSecret, customer, userEmail, reason string) (mailAPI, error) {
	c, err := googleworkspace.NewMailClient(ctx, cfg, sec, customer, userEmail)
	if err != nil {
		return nil, err
	}
	c.Reason = reason
	return c, nil
}

// mailAccess validates the confirmation, resolves the target inside the tenant
// and only then opens a mail client whose token subject is that user.
func (s *GoogleWorkspaceServiceServer) mailAccess(ctx context.Context, connectionID, tenantID, userID string, confirmation *neoboxv1.GmailAccessConfirmation) (mailAPI, string, error) {
	d, t, _, u, reason, err := s.gmailTarget(ctx, connectionID, tenantID, userID, confirmation)
	if err != nil {
		return nil, "", err
	}
	var cfg googleworkspace.ConnectionConfig
	var sec googleworkspace.ConnectionSecret
	if err := d.conns.Open(d.c, &cfg, &sec); err != nil {
		return nil, "", mapConnectionErr(err)
	}
	api, err := d.mail(ctx, cfg, sec, t.CustomerID, u.PrimaryEmail, reason)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeFailedPrecondition, err)
	}
	api.SetWriteRecorder(workspaceRecorder{repo: d.repo, connection: d.c, tenant: t, actorName: d.actorName})
	return api, u.PrimaryEmail, nil
}

// mapMailErr adds the mail-specific outcomes to the shared Workspace mapping.
// A missing gmail.readonly authorization is a rejected delegation
// (FailedPrecondition, never Unauthenticated, which signs the user out).
func mapMailErr(err error) error {
	switch {
	case errors.Is(err, googleworkspace.ErrMailNotFound):
		return connectx.NotFound("message or attachment not found")
	case errors.Is(err, googleworkspace.ErrAttachmentTooLarge):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return mapWorkspaceFeatureErr(err, googleworkspace.GmailReadonlyScope)
}

func (s *GoogleWorkspaceServiceServer) ListWorkspaceUserGmailLabels(ctx context.Context, req *connect.Request[neoboxv1.ListWorkspaceUserGmailLabelsRequest]) (*connect.Response[neoboxv1.ListWorkspaceUserGmailLabelsResponse], error) {
	in := req.Msg
	api, email, err := s.mailAccess(ctx, in.GetConnectionId(), in.GetTenantId(), in.GetUserId(), in.GetConfirmation())
	if err != nil {
		return nil, err
	}
	res, err := api.ListLabels(ctx)
	if err != nil {
		return nil, mapMailErr(err)
	}
	out := &neoboxv1.ListWorkspaceUserGmailLabelsResponse{UserEmail: email, ReadAt: timestamppb.New(res.ReadAt)}
	for _, l := range res.Labels {
		out.Labels = append(out.Labels, &neoboxv1.GmailMailLabel{Id: l.ID, Name: l.Name, Type: l.Type, CountsKnown: l.CountsKnown, MessagesTotal: int32(l.MessagesTotal), MessagesUnread: int32(l.MessagesUnread)})
	}
	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) SearchWorkspaceUserGmailMessages(ctx context.Context, req *connect.Request[neoboxv1.SearchWorkspaceUserGmailMessagesRequest]) (*connect.Response[neoboxv1.SearchWorkspaceUserGmailMessagesResponse], error) {
	in := req.Msg
	search := googleworkspace.MailSearch{Query: in.GetQuery(), LabelIDs: in.GetLabelIds(), IncludeSpamTrash: in.GetIncludeSpamTrash(), PageToken: in.GetPageToken(), PageSize: int(in.GetPageSize())}
	// Checked before the target is resolved: a bad query costs no Google call.
	if err := search.Validate(); err != nil {
		return nil, mapWorkspaceErr(err)
	}
	api, email, err := s.mailAccess(ctx, in.GetConnectionId(), in.GetTenantId(), in.GetUserId(), in.GetConfirmation())
	if err != nil {
		return nil, err
	}
	page, err := api.SearchMessages(ctx, search)
	if err != nil {
		return nil, mapMailErr(err)
	}
	out := &neoboxv1.SearchWorkspaceUserGmailMessagesResponse{UserEmail: email, NextPageToken: page.NextPageToken, ResultSizeEstimate: int32(page.ResultSizeEstimate), ReadAt: timestamppb.New(page.ReadAt)}
	for _, m := range page.Messages {
		out.Messages = append(out.Messages, &neoboxv1.GmailMessageSummary{
			Id: m.ID, ThreadId: m.ThreadID, From: m.From, To: m.To, Subject: m.Subject, Snippet: m.Snippet,
			Date: gmailTime(m.Date), LabelIds: m.LabelIDs, HasAttachment: m.HasAttachment, Unavailable: m.Unavailable,
		})
	}
	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) GetWorkspaceUserGmailMessage(ctx context.Context, req *connect.Request[neoboxv1.GetWorkspaceUserGmailMessageRequest]) (*connect.Response[neoboxv1.GetWorkspaceUserGmailMessageResponse], error) {
	in := req.Msg
	if !googleworkspace.ValidMailID(in.GetMessageId()) {
		return nil, connectx.InvalidArgument("message_id", "is not valid")
	}
	api, email, err := s.mailAccess(ctx, in.GetConnectionId(), in.GetTenantId(), in.GetUserId(), in.GetConfirmation())
	if err != nil {
		return nil, err
	}
	m, err := api.ReadMessage(ctx, in.GetMessageId())
	if err != nil {
		return nil, mapMailErr(err)
	}
	msg := &neoboxv1.GmailMessage{
		Id: m.ID, ThreadId: m.ThreadID, From: m.From, To: m.To, Cc: m.Cc, Bcc: m.Bcc, ReplyTo: m.ReplyTo, Subject: m.Subject,
		Date: gmailTime(m.Date), LabelIds: m.LabelIDs, Snippet: m.Snippet, PlainText: m.PlainText, Html: m.HTML,
		PlainTextFromHtml: m.PlainTextFromHTML, BodyTruncated: m.BodyTruncated,
	}
	for _, a := range m.Attachments {
		msg.Attachments = append(msg.Attachments, &neoboxv1.GmailAttachment{
			PartId: a.PartID, Filename: a.Filename, MimeType: a.MimeType, Size: a.Size, Inline: a.Inline,
			Downloadable: a.Downloadable, TooLarge: a.Size > googleworkspace.MaxAttachmentBytes,
		})
	}
	return connect.NewResponse(&neoboxv1.GetWorkspaceUserGmailMessageResponse{UserEmail: email, Message: msg, ReadAt: timestamppb.New(m.ReadAt)}), nil
}

func (s *GoogleWorkspaceServiceServer) DownloadWorkspaceUserGmailAttachment(ctx context.Context, req *connect.Request[neoboxv1.DownloadWorkspaceUserGmailAttachmentRequest]) (*connect.Response[neoboxv1.DownloadWorkspaceUserGmailAttachmentResponse], error) {
	in := req.Msg
	if !googleworkspace.ValidMailID(in.GetMessageId()) {
		return nil, connectx.InvalidArgument("message_id", "is not valid")
	}
	if !googleworkspace.ValidMailPartID(in.GetPartId()) {
		return nil, connectx.InvalidArgument("part_id", "is not valid")
	}
	api, _, err := s.mailAccess(ctx, in.GetConnectionId(), in.GetTenantId(), in.GetUserId(), in.GetConfirmation())
	if err != nil {
		return nil, err
	}
	file, err := api.DownloadAttachment(ctx, in.GetMessageId(), in.GetPartId())
	if err != nil {
		return nil, mapMailErr(err)
	}
	return connect.NewResponse(&neoboxv1.DownloadWorkspaceUserGmailAttachmentResponse{Filename: file.Filename, MimeType: file.MimeType, Data: file.Data}), nil
}
