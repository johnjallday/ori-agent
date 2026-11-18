package personalassistanthttp

import (
	"net/http"
	"testing"
)

func TestHandlerSetupHQ_PreservesOptionalScheduleChoice(t *testing.T) {
	for _, choice := range []string{"", `,"schedule_enabled":false`, `,"schedule_enabled":true`} {
		t.Run(choice, func(t *testing.T) {
			service := &fakeHQSetupService{result: activeHQResult()}
			response := postHQ(t, hqHandler(t, service), `{"request_id":"owned-hq","if_version":4,"name":"Owned HQ"`+choice+`}`)
			if response.Code != http.StatusCreated || service.calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, service.calls, response.Body.String())
			}
			got := service.request.ScheduleEnabled
			if choice == "" {
				if got != nil {
					t.Fatal("absent choice became explicit")
				}
				return
			}
			if got == nil || *got != (choice == `,"schedule_enabled":true`) {
				t.Fatalf("wrong schedule choice: %v", got)
			}
		})
	}
}

func TestHandlerSetupHQ_RejectsInvalidScheduleChoiceBeforeSetup(t *testing.T) {
	for _, value := range []string{`"false"`, `1`, `{}`, `[]`} {
		t.Run(value, func(t *testing.T) {
			service := &fakeHQSetupService{result: activeHQResult()}
			response := postHQ(t, hqHandler(t, service), `{"request_id":"owned-hq","if_version":4,"schedule_enabled":`+value+`}`)
			if response.Code != http.StatusBadRequest || service.calls != 0 {
				t.Fatalf("status=%d calls=%d", response.Code, service.calls)
			}
		})
	}
}
