package handler

import "github.com/slackerkids/agent-memo.git/internal/contract"

func ResolveUserID(userID *string, sessionID string) string {
	return contract.ResolveUserID(userID, sessionID)
}
