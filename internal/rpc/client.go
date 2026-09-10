package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

const responseMaxBytes = 8 << 20

func Call(ctx context.Context, socketPath string, req Request) (Response, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return Response{}, fmt.Errorf("connect to broker: %w", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(70 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	payload, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("encode request: %w", err)
	}
	if err := WriteFrame(conn, payload); err != nil {
		return Response{}, err
	}
	payload, err = ReadFrame(conn, responseMaxBytes)
	if err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.Unmarshal(payload, &resp); err != nil {
		return Response{}, fmt.Errorf("decode response: %w", err)
	}
	if resp.V != Version1 || resp.ID != req.ID {
		return Response{}, fmt.Errorf("broker returned a mismatched response")
	}
	return resp, nil
}
