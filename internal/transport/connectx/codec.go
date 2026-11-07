package connectx

import (
	"context"
	"fmt"
	"io"

	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// snakeCaseJSONCodec replaces Connect's default "json" codec with one that
// emits proto field names (snake_case) instead of JSON names (camelCase) on
// the wire. The previous Twirp transport defaulted to snake_case JSON, and
// 130+ dashboard call sites read response fields like `connected_daemons` /
// `base_url` / `space_id` without a camelCase fallback. Until those callers
// are migrated to typed Connect clients (which use camelCase natively), the
// server must keep speaking snake_case so the dashboard stays functional.
//
// The unmarshal direction sets DiscardUnknown=true to remain forward
// compatible with clients that send additional fields, mirroring Connect's
// default behavior.
type snakeCaseJSONCodec struct {
	name string
}

func newSnakeCaseJSONCodec(name string) *snakeCaseJSONCodec {
	return &snakeCaseJSONCodec{name: name}
}

func (c *snakeCaseJSONCodec) Name() string { return c.name }

func (c *snakeCaseJSONCodec) MarshalWrite(_ context.Context, dst io.Writer, message any) error {
	msg, ok := message.(proto.Message)
	if !ok {
		return fmt.Errorf("connectx: marshal: expected proto.Message, got %T", message)
	}
	raw, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = dst.Write(raw)
	return err
}

func (c *snakeCaseJSONCodec) UnmarshalRead(_ context.Context, src io.Reader, message any) error {
	msg, ok := message.(proto.Message)
	if !ok {
		return fmt.Errorf("connectx: unmarshal: expected proto.Message, got %T", message)
	}
	data, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		// protojson rejects empty input; Connect peers send "{}" for empty
		// messages but be lenient with literal empty bodies too.
		return nil
	}
	return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(data, msg)
}

// RPCMaxRequestBytes is the largest single RPC request message the server
// accepts. connect-go v2 defaults to 4 MiB; the limit is stated explicitly so
// it is a deliberate choice rather than an inherited default. Larger requests
// fail with CodeResourceExhausted before the handler runs. Snapshot downloads
// are plain HTTP streams outside the RPC layer and are not affected.
const RPCMaxRequestBytes = 4 << 20

// HandlerOptions returns the connecthttp option slice that should be applied
// to the shared Connect server so responses keep their legacy snake_case JSON
// shape. connecthttp.WithCodecs replaces the default codec set instead of
// adding to it, so the binary protobuf codec Connect-Web clients use is listed
// explicitly. connect-go canonicalizes "application/json; charset=utf-8" to
// the bare "json" codec, so one registration covers both content types.
func HandlerOptions() []connecthttp.Option {
	return []connecthttp.Option{
		connecthttp.WithCodecs(
			newSnakeCaseJSONCodec("json"),
			connectproto.NewBinaryCodec(),
		),
		connecthttp.WithReadMaxBytes(RPCMaxRequestBytes),
	}
}
