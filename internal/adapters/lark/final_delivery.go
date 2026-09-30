package lark

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/channel/outbound"
)

func deliveryID(opts SendOptions) string {
	id, _ := opts.Metadata["deliveryId"].(string)
	return id
}

func (t *OAPITransport) sendFinalMessage(ctx context.Context, req SendMessageRequest) (SendResult, error) {
	contents := []MessageContent{req.Content}
	if req.Content.Markdown != "" && req.Content.Card == nil {
		contents = nil
		for _, chunk := range outbound.SplitWithCodeFences(req.Content.Markdown, 3500) {
			contents = append(contents, MessageContent{Markdown: chunk})
		}
	}
	var first SendResult
	for _, content := range contents {
		msgType, body, err := directMessageContent(content)
		if err != nil {
			return first, err
		}
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return first, err
		}
		opts := req.Options
		opts.Metadata = map[string]any{"deliveryId": hex.EncodeToString(id[:])}
		var result SendResult
		for attempt := 0; attempt < 3; attempt++ {
			if err := ctx.Err(); err != nil {
				return first, err
			}
			result, err = t.sendDirect(ctx, req.ChatID, msgType, body, opts)
			if err == nil || !retryableDeliveryError(err) {
				break
			}
			if attempt < 2 {
				timer := time.NewTimer(time.Duration(attempt+1) * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					return first, ctx.Err()
				case <-timer.C:
				}
			}
		}
		if err != nil {
			return first, err
		}
		if first.MessageID == "" {
			first = result
		}
	}
	return first, nil
}

func retryableDeliveryError(err error) bool {
	var network net.Error
	if errors.As(err, &network) {
		return true
	}
	var api *OAPIError
	return errors.As(err, &api) && api.Code == 99991400
}
