package server

import (
	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/profile"
)

func testAIRuntime(userID int64, provider ai.Provider, model string) *AIRuntime {
	return &AIRuntime{
		UserID:             userID,
		Provider:           provider,
		DealbreakerVersion: ai.DealbreakerVersion(provider.Name(), model),
		ScoreVersion:       ai.ScoreVersion(provider.Name(), model),
		RunTokenCap:        defaultRunTokenCap,
		DailyTokenCap:      profile.DefaultDailyTokenCap,
		MonthlyTokenCap:    aiMonthlyTokenCapForUSDCents(profile.DefaultAIMonthlyUSDCents),
		PerCallCap:         profile.DefaultAIPerCallCap,
	}
}
