package server

// AutomationCatalogEntry is a ready-made automation template (static, not stored in DB).
type AutomationCatalogEntry struct {
	Key            string `json:"key"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	SuggestedCron  string `json:"suggested_cron"`
	PromptTemplate string `json:"prompt_template"`
	SkillRef       string `json:"skill_ref,omitempty"`
}

// automationCatalog is the built-in template list for GET /api/automations/catalog.
var automationCatalog = []AutomationCatalogEntry{
	{
		Key:            "daily-followup",
		Title:          "Daily follow-up",
		Description:    "Weekday morning digest of open items and suggested next steps for the channel.",
		SuggestedCron:  "0 9 * * 1-5",
		PromptTemplate: "Review open tasks and recent channel context. Send a concise daily follow-up: what needs attention today, blockers, and one recommended next action.",
		SkillRef:       "playground",
	},
	{
		Key:            "weekly-report",
		Title:          "Weekly report",
		Description:    "Monday morning summary of the past week for stakeholders on this channel.",
		SuggestedCron:  "0 8 * * 1",
		PromptTemplate: "Summarize the past week: completed work, in-progress items, risks, and priorities for the coming week. Keep it brief and scannable.",
		SkillRef:       "playground",
	},
	{
		Key:            "lead-triage",
		Title:          "Lead triage",
		Description:    "Hourly check for new leads or urgent messages that need a quick response.",
		SuggestedCron:  "@hourly",
		PromptTemplate: "Check for new inbound leads or urgent messages. If anything needs a human reply within the hour, summarize it with suggested responses; otherwise reply NO_REPLY.",
		SkillRef:       "playground",
	},
}
