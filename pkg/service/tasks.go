package service

// The registry's own A2A skill names (07).
const (
	skillSearchAgents  = "search-agents"
	skillGetAgent      = "get-agent"
	skillRegisterAgent = "register-agent"
)

// KnownSkills are the registry's own A2A skills (07).
//
// The A2A task lifecycle itself (SendMessage/GetTask/CancelTask, async
// register-agent execution, restart recovery) is owned end to end by the
// official A2A SDK (github.com/a2aproject/a2a-go/v2/a2asrv) via
// pkg/a2aserver's AgentExecutor, using the SDK's in-memory task store —
// see that package's Mount for the fit-assessment behind that choice.
// There is no a2a_tasks table: a hand-rolled CreateTask/GetTask/CancelTask/
// TaskWorker implementation backing one existed briefly during
// development but was removed once the SDK adoption made it dead code
// (docs/03, docs/07).
var KnownSkills = map[string]bool{
	skillSearchAgents:  true,
	skillGetAgent:      true,
	skillRegisterAgent: true,
}
