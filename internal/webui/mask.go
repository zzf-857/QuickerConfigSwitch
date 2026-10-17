package webui

import (
	"github.com/zzf-857/quickerconfigswitch/internal/core"
)

// maskText masks API keys in text shown to the user (see core.Redact).
func maskText(s string) string { return core.Redact(s) }

func maskPreview(p core.Preview) core.Preview {
	p.Diff, p.Notice = maskText(p.Diff), maskText(p.Notice)
	return p
}

func maskSteps(steps []core.ProfileStep) []core.ProfileStep {
	for i := range steps {
		steps[i].Preview = maskPreview(steps[i].Preview)
		steps[i].Error = maskText(steps[i].Error)
		if r := steps[i].Result; r != nil {
			r.Notice = maskText(r.Notice)
		}
	}
	return steps
}
