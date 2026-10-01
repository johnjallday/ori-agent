package server

import (
	agenthttp "github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/projectstaffing"
)

// carriedEdits lets the Agents page's edit reach the project copies a Home's
// standing consent tracks (D10), without agenthttp knowing about consents.
type carriedEdits struct {
	service *projectstaffing.Service
}

func (c carriedEdits) CarryEdit(agentName string) (agenthttp.CarriedEdit, error) {
	report, err := c.service.Carry(agentName)
	return agenthttp.CarriedEdit{Updated: report.Updated, Customised: report.Customised}, err
}

func (c carriedEdits) InStep(agentName string) map[string]bool { return c.service.InStep(agentName) }
