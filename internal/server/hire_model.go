package server

import (
	"slices"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/llm"
)

// hireRunnableModel is the model an agent hired from the Agents page runs on
// (agents-hire-with-a-brief): its catalog tier's model on a registered
// provider that offers it, else the system model when that can run. Without
// it a tier model whose provider was never set up passes the goal-run check
// (an agent with no provider borrows the system model there) and then fails
// when the first look runs, because execution infers the provider from the
// model's name. Nothing runnable leaves the tier's model as catalog create
// would, and the look says why it cannot start.
func hireRunnableModel(factory *llm.Factory, systemModel func() (string, string)) agenthttp.RunnableModelChooser {
	return func(tierModel string) (string, string) {
		tierModel = strings.TrimSpace(tierModel)
		if factory == nil {
			return "", tierModel
		}
		if tierModel != "" {
			providers := factory.ListProviders()
			sort.Slice(providers, func(i, j int) bool { return providers[i].Name < providers[j].Name })
			for _, info := range providers {
				if slices.Contains(info.Models, tierModel) {
					return info.Name, tierModel
				}
			}
		}
		if systemModel != nil {
			provider, model := systemModel()
			provider = strings.ToLower(strings.TrimSpace(provider))
			if result, err := factory.GetSystemModelProvider(provider, strings.TrimSpace(model)); err == nil && result != nil {
				return provider, result.Model
			}
		}
		return "", tierModel
	}
}
