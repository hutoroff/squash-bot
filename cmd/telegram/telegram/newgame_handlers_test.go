package telegram

import (
	"testing"

	"github.com/hutoroff/squash-bot/internal/models"
)

func TestPrepareWizardVenueSportSelection(t *testing.T) {
	single := &newGameWizard{}
	prepareWizardVenue(single, &models.Venue{Sports: []models.VenueSport{{Sport: "padel", Courts: "1,2"}}})
	if single.step != wizardStepCourtPick || single.sport != "padel" {
		t.Fatalf("single sport should skip picker: step=%v sport=%q", single.step, single.sport)
	}

	multiple := &newGameWizard{}
	prepareWizardVenue(multiple, &models.Venue{Sports: []models.VenueSport{
		{Sport: "squash", Courts: "1,2"},
		{Sport: "bowling", Courts: "A,B"},
	}})
	if multiple.step != wizardStepSport {
		t.Fatalf("multiple sports should show picker: step=%v", multiple.step)
	}
}
