package foldersetup

import (
	"context"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"testing"
)

func TestPortfolioDestination_FencesEveryLibraryReviewCommit(t *testing.T) {
	for _, point := range []struct{ review, commit string }{{"library:review", "library:on"}, {"root:review", "root:connect"}, {"scan:review", "scan:commit"}} {
		t.Run(point.review, func(t *testing.T) {
			w := newPortfolioWorld()
			w.homeID = "home-1"
			facts := freshFacts()
			facts.HomeExists = true
			facts.Destination = &personalassistant.FolderSetupDestination{Status: "existing", WorkspaceID: w.homeID, Name: facts.HomeName, Kind: "home", OwnerUserID: "local"}
			result, _ := runPortfolio(t, w, portfolioPlanFor(facts), func(r *PortfolioRunner, c *PortfolioConfig) {
				r.ValidateDestination = func(context.Context, string) error {
					if w.called(point.review) {
						return personalassistant.ErrFolderPlanChanged
					}
					return nil
				}
			})
			if result.StopReason != personalassistant.FolderStopPlanChanged || w.called(point.commit) || w.receiptFacts != nil {
				t.Fatalf("stale commit or receipt: %+v %v", result, w.log)
			}
		})
	}
}
func TestPortfolioDestination_NewHomeRequiresNameAndResultingIdentity(t *testing.T) {
	for _, name := range []string{"Music Production Home", "Different Home"} {
		t.Run(name, func(t *testing.T) {
			w := newPortfolioWorld()
			facts := freshFacts()
			facts.Destination = &personalassistant.FolderSetupDestination{Status: "new", Name: name, Kind: "home", OwnerUserID: "local"}
			result, progress := runPortfolio(t, w, portfolioPlanFor(facts), nil)
			if name == facts.HomeName {
				if result.Status != personalassistant.FolderSetupDone || progress.updates[len(progress.updates)-1].HomeID != w.homeID {
					t.Fatal("missing resulting-parent receipt", result)
				}
			} else if result.StopReason != personalassistant.FolderStopPlanChanged || w.called("home:commit") {
				t.Fatal("changed name created a Home", result, w.log)
			}
		})
	}
	// A Home created recently by another operation is not this run's identity.
	w := newPortfolioWorld()
	w.homeID = "unreceipted"
	facts := freshFacts()
	facts.Destination = &personalassistant.FolderSetupDestination{Status: "new", Name: facts.HomeName, Kind: "home", OwnerUserID: "local"}
	result, _ := runPortfolio(t, w, portfolioPlanFor(facts), nil)
	if result.StopReason != personalassistant.FolderStopPlanChanged || w.called("library:on") {
		t.Fatal("adopted unreceipted Home", result, w.log)
	}
}
func TestPortfolioDestination_RevocationStopsStaffingSharingAndReceipt(t *testing.T) {
	w := newPortfolioWorld()
	w.homeID = "home-1"
	facts := freshFacts()
	facts.HomeExists = true
	facts.Destination = &personalassistant.FolderSetupDestination{Status: "existing", WorkspaceID: w.homeID, Name: facts.HomeName, Kind: "home", OwnerUserID: "local"}
	result, _ := runPortfolio(t, w, portfolioPlanFor(facts), func(r *PortfolioRunner, c *PortfolioConfig) {
		r.ValidateDestination = func(context.Context, string) error { return personalassistant.ErrFolderPlanChanged }
	})
	if result.StopReason != personalassistant.FolderStopPlanChanged || len(w.log) != 0 || w.receiptFacts != nil {
		t.Fatal("revoked destination used", result, w.log)
	}
}
