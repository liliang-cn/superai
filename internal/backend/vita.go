package backend

// Vita is the person's health as the Vita iPhone app reads it from Apple
// Health: one summary per day (sleep, resting heart rate, HRV, activity,
// Vita's recovery and stress scores and suggestions), kept by a vita server
// the app reports to. Like noted it is an MCP server in mcpServers.json,
// usually remote over HTTP with "Authorization": "Bearer ${VITA_TOKEN}"; and
// it is optional the same way: an entry whose token is not set is left out,
// so a SuperLeo without VITA_TOKEN knows nothing of it.

// VitaServer is the name Vita is configured under in mcpServers.json.
const VitaServer = "vita"

// vitaSection tells the model where health questions go when Vita is
// connected.
func vitaSection(configured bool) string {
	if !configured {
		return ""
	}
	return `

The person's health lives in Vita (tools mcp_vita_*), read from Apple Health on their iPhone, one summary per day: sleep, resting heart rate, HRV, blood oxygen, steps and activity, weight, Vita's recovery and stress scores, and its insights and suggestions. For "how did I sleep", "is my resting heart rate up", "how active was I this week" use health_today, health_days or health_trend, never memory, and answer from the numbers with their dates. A day only has what the phone knew; a missing value is not a zero. health_today says when the phone last reported: if that is old, say so. Give the numbers and what Vita said about them, plainly; this is not medical advice, and anything worrying is for a doctor.`
}
