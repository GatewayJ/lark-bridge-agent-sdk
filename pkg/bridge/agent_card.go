package bridge

import (
	"context"
	"errors"

	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/cardauth"
	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/cardkit"
	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/impresenter"
	appintake "github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/intake"
)

func (i *managedLarkIntake) agentCardSender(metadata RunMetadata, first appintake.MessageInput, options impresenter.SendOptions) func(context.Context, map[string]any) error {
	return func(ctx context.Context, request map[string]any) error {
		if i.callbackAuth == nil || i.callbackAuth.auth == nil {
			return ErrNilCallbackAuth
		}
		card, values, err := cardkit.PrepareAgentCard(request)
		if err != nil {
			return err
		}
		token, err := i.callbackAuth.auth.SignPending(cardauth.SignInput{
			RunID: metadata.RunID, Scope: metadata.ScopeID, ChatID: first.ChatID,
			OperatorOpenID: first.Sender.OpenID, Action: "agent_callback",
			PolicyFingerprint: metadata.PolicyFingerprint, TTL: i.callbackTTL,
		}, values)
		if err != nil {
			return err
		}
		for _, value := range values {
			value[BridgeCardCallbackMarker] = true
			value[BridgeCardTokenKey] = token
		}
		_, err = i.presenterChannel().SendCard(ctx, impresenter.SendCardRequest{ChatID: first.ChatID, Options: options, Card: card})
		if err != nil {
			return errors.Join(err, i.callbackAuth.auth.CancelPending(token))
		}
		return nil
	}
}

func (i *managedLarkIntake) pendingCardVerifier() CardCallbackVerifier {
	if i.callbackAuth == nil || i.callbackAuth.auth == nil {
		return nil
	}
	return CardCallbackVerifierFunc(func(_ context.Context, token string, expected CardCallbackVerifyExpected) CardCallbackVerifyResult {
		result := i.callbackAuth.auth.VerifyPending(token, cardauth.VerifyExpected{
			Scope: expected.Scope, ChatID: expected.ChatID, OperatorOpenID: expected.OperatorOpenID, Action: expected.Action,
		}, expected.Value)
		return CardCallbackVerifyResult{OK: result.OK, Reason: string(result.Reason)}
	})
}
