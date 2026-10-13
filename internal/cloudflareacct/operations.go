package cloudflareacct

import (
	"context"
	"fmt"

	"butterfly.orx.me/core/log"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	cfrepo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
)

// resourceChange is one change to a Worker or a Pages project, about to be
// sent.
type resourceChange struct {
	kind     cfrepo.ResourceKind
	resource string
	action   string
	// target is what the action applies to; see ResourceOperation.Target.
	target string
	// before, requested: what the log keeps. They must never hold secret
	// values.
	before, requested map[string]any
	// what and permission name the change for a refused token's error.
	what, permission string
	// send makes the change and returns the state after it, nil when there
	// is none to report.
	send func(context.Context) (after map[string]any, err error)
}

// OperationResult is a change that was sent and succeeded.
type OperationResult struct {
	// Operation is the logged operation; nil when nothing needed doing.
	Operation *cfrepo.ResourceOperation
	// LogErr is set when the outcome could not be logged; the change was
	// made all the same and the log shows it pending.
	LogErr error
}

// operate logs the change as pending, sends it once, and logs its outcome,
// like change does for DNS records: without the pending entry nothing is
// sent, and a change whose answer never came is logged as unknown and not
// sent again.
func (a *Account) operate(ctx context.Context, actor Actor, c resourceChange) (*OperationResult, error) {
	logger := log.FromContext(ctx)
	op := &cfrepo.ResourceOperation{
		ID: a.m.newID(), UserID: a.conn.UserID, ConnectionID: a.conn.ID, AccountID: a.ID,
		Kind: c.kind, Resource: c.resource, Action: c.action, Target: c.target,
		Before: c.before, Requested: c.requested,
		Status: cfrepo.StatusPending, ActorID: actor.ID, ActorName: actor.Name, CreatedAt: a.m.now().UTC(),
	}
	if err := a.m.repo.CreateResourceOperation(ctx, op); err != nil {
		logger.Error("log resource operation failed", "connection_id", a.conn.ID, "resource", c.resource, "action", c.action, "err", err)
		return nil, fmt.Errorf("%w: %v", ErrLogUnavailable, err)
	}

	wctx, cancel := writeContext(ctx)
	after, err := c.send(wctx)
	cancel()
	switch {
	case err == nil:
		op.Status, op.After = cfrepo.StatusSucceeded, after
	case cloudflare.IsUncertain(err):
		op.Status = cfrepo.StatusUnknown
	default:
		op.Status = cfrepo.StatusFailed
	}
	if err != nil {
		op.Error = truncate(err.Error(), maxErrorChars)
	}
	op.FinishedAt = a.m.now().UTC()

	var logErr error
	if ferr := a.m.repo.FinishResourceOperation(context.WithoutCancel(ctx), op); ferr != nil {
		logger.Error("log resource operation outcome failed", "operation_id", op.ID, "status", op.Status, "err", ferr)
		logErr = ferr
	}
	if err != nil {
		return nil, a.classify(ctx, err, c.what, c.permission)
	}
	return &OperationResult{Operation: op, LogErr: logErr}, nil
}

// noChange is the result of an operation that found nothing to do.
func noChange() *OperationResult { return &OperationResult{} }
