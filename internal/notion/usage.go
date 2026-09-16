package notion

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// Маршруты сняты с mcp.har: именно их дергает веб-клиент Notion,
// когда открываешь экран AI Usage.
const (
	pathCreditRateLimit = "/api/v3/getCreditRateLimitStatus"
	pathUsageEligibility = "/api/v3/getAIUsageEligibilityV2"
)

// UsageWindow — одно окно лимита (rolling 6h или биллинговый период).
type UsageWindow struct {
	CreditType string  `json:"creditType"`
	Scope      string  `json:"scope"`
	Window     string  `json:"window,omitempty"`
	Cadence    string  `json:"cadence,omitempty"`
	Used       float64 `json:"used"`
	Limit      float64 `json:"limit"`
	PeriodEnd  int64   `json:"periodEndMs,omitempty"`
}

// Percent — заполненность окна в процентах (0..100+).
func (w UsageWindow) Percent() float64 {
	if w.Limit <= 0 {
		return 0
	}
	return w.Used / w.Limit * 100
}

// Usage — всё, что нужно UI для двух прогресс-баров и красного воркспейса.
type Usage struct {
	SpaceID string `json:"spaceId"`
	// Статус rate-limit: within_limit | rate_limited | …
	Status string `json:"status"`
	// Rolling-окно (обычно 6h) и окно биллингового периода.
	Rolling *UsageWindow `json:"rolling,omitempty"`
	Monthly *UsageWindow `json:"monthly,omitempty"`
	// Сколько секунд до сброса rolling-окна.
	ResetsInSeconds int64  `json:"resetsInSeconds"`
	CreditTier      string `json:"creditTier,omitempty"`

	// Премиальные кредиты (AI Usage в настройках Notion).
	PremiumUsed    float64 `json:"premiumUsed"`
	PremiumLimit   float64 `json:"premiumLimit"`
	CreditBalance  float64 `json:"creditBalance"`
	CreditsOverage float64 `json:"creditsInOverage"`
	PeriodStartMs  int64   `json:"servicePeriodStartMs,omitempty"`
	PeriodEndMs    int64   `json:"servicePeriodEndMs,omitempty"`

	// Базовые (free) кредиты за текущий период.
	BasicSpaceUsed  float64 `json:"basicSpaceUsed"`
	BasicSpaceLimit float64 `json:"basicSpaceLimit"`
	BasicUserUsed   float64 `json:"basicUserUsed"`
	BasicUserLimit  float64 `json:"basicUserLimit"`

	// Счётчики ·за всё время· (usage.lifetime). Именно их раньше по ошибке
	// сравнивали с месячными лимитами, оттуда брался ложный «лимит».
	LifetimeSpaceUsed float64 `json:"lifetimeSpaceUsed"`
	LifetimeUserUsed  float64 `json:"lifetimeUserUsed"`

	// LimitReached — воркспейс в лимитах: именно по этому флагу UI
	// красит воркспейс до даты сброса.
	LimitReached bool  `json:"limitReached"`
	ResetAtMs    int64 `json:"resetAtMs,omitempty"`

	Error string `json:"error,omitempty"`
}

func num(raw interface{}) float64 {
	switch v := raw.(type) {
	case float64:
		return v
	case json.Number:
		f, _ := v.Float64()
		return f
	}
	return 0
}

func mapOf(raw interface{}) map[string]interface{} {
	if m, ok := raw.(map[string]interface{}); ok {
		return m
	}
	return nil
}

func windowOf(raw interface{}) *UsageWindow {
	m := mapOf(raw)
	if m == nil {
		return nil
	}
	out := &UsageWindow{
		Used:  num(m["used"]),
		Limit: num(m["limit"]),
	}
	if s, ok := m["creditType"].(string); ok {
		out.CreditType = s
	}
	if s, ok := m["scope"].(string); ok {
		out.Scope = s
	}
	if s, ok := m["window"].(string); ok {
		out.Window = s
	}
	if s, ok := m["cadence"].(string); ok {
		out.Cadence = s
	}
	out.PeriodEnd = int64(num(m["periodEndMs"]))
	return out
}

// okStatus — статусы getCreditRateLimitStatus, при которых лимит НЕ исчерпан.
// «not_applicable» отдаёт Notion для воркспейсов без rate-limit (например,
// только что созданный личный воркспейс) — это норма, не лимит.
func okStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "within_limit", "not_applicable", "unlimited", "ok", "eligible", "allowed":
		return true
	}
	return false
}

// AIUsage собирает rolling- и monthly-лимиты по двум запросам из mcp.har.
func (c *Client) AIUsage(ctx context.Context, spaceID string) (Usage, error) {
	out := Usage{SpaceID: spaceID}
	if strings.TrimSpace(spaceID) == "" {
		return out, errors.New("не выбран воркспейс")
	}

	body := map[string]interface{}{"spaceId": spaceID}

	rate, rateErr := c.PostJSON(ctx, pathCreditRateLimit, body)
	if rateErr == nil {
		if s, ok := rate["status"].(string); ok {
			out.Status = s
		}
		if s, ok := rate["creditTier"].(string); ok {
			out.CreditTier = s
		}
		out.ResetsInSeconds = int64(num(rate["resetsInSeconds"]))
		out.Rolling = windowOf(rate["window"])
		out.Monthly = windowOf(rate["billingPeriodWindow"])
		if out.Monthly != nil {
			out.PeriodEndMs = out.Monthly.PeriodEnd
		}
	}

	usage, usageErr := c.PostJSON(ctx, pathUsageEligibility, body)
	if usageErr == nil {
		if premium := mapOf(usage["premiumCredits"]); premium != nil {
			out.CreditBalance = num(premium["totalCreditBalance"])
			out.CreditsOverage = num(premium["creditsInOverage"])
			out.PeriodStartMs = int64(num(premium["servicePeriodStartMs"]))
			if per := mapOf(premium["perSource"]); per != nil {
				for _, key := range []string{"monthlyAllocated", "monthlyCommitted", "stackedTrial", "yearlyElastic"} {
					if src := mapOf(per[key]); src != nil {
						out.PremiumUsed += num(src["usageTotal"])
						out.PremiumLimit += num(src["limit"])
					}
				}
			}
		}
		if basic := mapOf(usage["basicCredits"]); basic != nil {
			out.BasicSpaceUsed = num(basic["spaceUsage"])
			out.BasicSpaceLimit = num(basic["spaceLimit"])
			out.BasicUserUsed = num(basic["userUsage"])
			out.BasicUserLimit = num(basic["userLimit"])
		}
		// basicCredits.*Usage — счётчики за всё время (в старом воркспейсе
		// это десятки тысяч при лимите 525). Для баров берём текущий период.
		if root := mapOf(usage["usage"]); root != nil {
			if life := mapOf(root["lifetime"]); life != nil {
				out.LifetimeSpaceUsed = num(life["spaceUsage"])
				out.LifetimeUserUsed = num(life["userUsage"])
			}
			if current := mapOf(root["currentServicePeriod"]); current != nil {
				out.BasicSpaceUsed = num(current["spaceUsage"])
				out.BasicUserUsed = num(current["userUsage"])
			}
		}
	}

	if rateErr != nil && usageErr != nil {
		out.Error = rateErr.Error()
		return out, rateErr
	}

	// Если лимиты премиум-кредитов пришли только из eligibility,
	// строим monthly-окно из них — иначе в UI был бы пустой бар.
	if out.Monthly == nil && out.PremiumLimit > 0 {
		out.Monthly = &UsageWindow{
			CreditType: "premium_credits",
			Scope:      "space",
			Cadence:    "billing_period",
			Used:       out.PremiumUsed,
			Limit:      out.PremiumLimit,
			PeriodEnd:  out.PeriodEndMs,
		}
	}

	// В лимитах считаемся только по явно плохому статусу или заполненному окну.
	// Раньше здесь было «status != within_limit», и свежий воркспейс с
	// ответом «not_applicable» сразу красился как исчерпанный.
	if !okStatus(out.Status) {
		out.LimitReached = true
	}
	if out.CreditsOverage > 0 {
		out.LimitReached = true
	}
	if out.BasicSpaceLimit > 0 && out.BasicSpaceUsed >= out.BasicSpaceLimit {
		out.LimitReached = true
	}
	if out.BasicUserLimit > 0 && out.BasicUserUsed >= out.BasicUserLimit {
		out.LimitReached = true
	}
	if out.Monthly != nil && out.Monthly.Limit > 0 && out.Monthly.Used >= out.Monthly.Limit {
		out.LimitReached = true
	}
	if out.Rolling != nil && out.Rolling.Limit > 0 && out.Rolling.Used >= out.Rolling.Limit {
		out.LimitReached = true
	}

	// Дата, до которой воркспейс остаётся красным.
	if out.Monthly != nil && out.Monthly.PeriodEnd > 0 {
		out.ResetAtMs = out.Monthly.PeriodEnd
	} else if out.PeriodEndMs > 0 {
		out.ResetAtMs = out.PeriodEndMs
	}

	return out, nil
}
