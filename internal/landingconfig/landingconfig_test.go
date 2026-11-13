package landingconfig_test

import (
	"strings"
	"testing"

	"gitpair/internal/landingconfig"
)

// The declaration is one table and two keys, and every deviation from it is a
// defect that resolves the whole file to the default route. These tables pin
// both halves: what a valid declaration resolves to, and what each defect is
// called when the fallback is taken.

func TestParseResolvesTheDeclaredRoute(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		route landingconfig.Route
		next  string
	}{
		{"absent file", "", landingconfig.RouteDefault, ""},
		{"comments only", "# nothing declared\n", landingconfig.RouteDefault, ""},
		{"explicit default", "[landing]\nroute = \"default\"\n", landingconfig.RouteDefault, ""},
		{"manual", "[landing]\nroute = \"manual\"\n", landingconfig.RouteManual, ""},
		{"automated", "[landing]\nroute = \"automated\"\n", landingconfig.RouteAutomated, ""},
		{"custom with template", "[landing]\nroute = \"custom\"\nnext = \"open the PR into {destination}\"\n",
			landingconfig.RouteCustom, "open the PR into {destination}"},
		{"bare value", "[landing]\nroute = manual\n", landingconfig.RouteManual, ""},
		{"comments and blanks around the table", "# the merge is a job's\n\n[landing]\n\nroute = \"automated\" # the integrate job\n",
			landingconfig.RouteAutomated, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := landingconfig.Parse(tt.text)
			if got.Route != tt.route {
				t.Errorf("route = %q, want %q", got.Route, tt.route)
			}
			if got.Next != tt.next {
				t.Errorf("next = %q, want %q", got.Next, tt.next)
			}
			if got.Defect != "" {
				t.Errorf("defect = %q, want none", got.Defect)
			}
		})
	}
}

func TestParseReportsEachDefectAndFallsBackToDefault(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		defect string
	}{
		{"unknown route", "[landing]\nroute = \"ci\"\n", `unknown landing route "ci"`},
		{"unknown key", "[landing]\nroute = \"manual\"\nowner = \"job\"\n", `unknown landing key "owner"`},
		{"unknown section", "[merge]\nroute = \"manual\"\n", "unknown section [merge]"},
		{"key outside the table", "route = \"manual\"\n", `outside [landing]`},
		{"malformed line", "[landing]\nroute manual\n", `expected key = "value"`},
		{"unclosed section header", "[landing\nroute = \"manual\"\n", "does not close its section header"},
		{"custom without a template", "[landing]\nroute = \"custom\"\n", `route is "custom" but next is empty`},
		{"template under another route", "[landing]\nroute = \"manual\"\nnext = \"open the PR\"\n",
			`next is read only when route is "custom"`},
		{"template with no route", "[landing]\nnext = \"open the PR\"\n", `next is read only when route is "custom"`},
		{"unknown placeholder", "[landing]\nroute = \"custom\"\nnext = \"ask {forge} about {destination}\"\n",
			`"forge"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := landingconfig.Parse(tt.text)
			if got.Route != landingconfig.RouteDefault {
				t.Errorf("route = %q, want the default fallback", got.Route)
			}
			if got.Next != "" {
				t.Errorf("next = %q, want it dropped with the route", got.Next)
			}
			if !strings.Contains(got.Defect, tt.defect) {
				t.Errorf("defect = %q, want it to name %q", got.Defect, tt.defect)
			}
		})
	}
}

// A template naming two unknown placeholders names both, once each, so one
// fix pass over the file silences the warning.
func TestParseNamesEachUnknownPlaceholderOnce(t *testing.T) {
	got := landingconfig.Parse("[landing]\nroute = \"custom\"\nnext = \"{forge} then {forge} then {destination}\"\n")
	if got.Route != landingconfig.RouteDefault {
		t.Errorf("route = %q, want the default fallback", got.Route)
	}
	if !strings.Contains(got.Defect, `"forge"`) || strings.Contains(got.Defect, `"forge", "forge"`) {
		t.Errorf("defect = %q, want forge named once", got.Defect)
	}
	if strings.Contains(got.Defect, `"destination"`) {
		t.Errorf("defect = %q, want the known placeholder left out", got.Defect)
	}
}

// The known placeholders are exactly the three facts git-pair holds for every
// changeset. Expand fills them with whatever the sentence layer passes —
// including the display fallbacks — and touches nothing else, because Parse
// has already refused a template naming anything else.
func TestExpandFillsTheKnownPlaceholders(t *testing.T) {
	got := landingconfig.Expand("push {branch}, open the PR into {destination} for {changeset}",
		"feat-x", "main", "feat-x")
	want := "push feat-x, open the PR into main for feat-x"
	if got != want {
		t.Errorf("Expand = %q, want %q", got, want)
	}
	if out := landingconfig.Expand("no placeholders here", "feat-x", "main", "feat-x"); out != "no placeholders here" {
		t.Errorf("Expand = %q, want the template through unchanged", out)
	}
}

func TestRouteExits(t *testing.T) {
	tests := []struct {
		route         landingconfig.Route
		offersMerge   bool
		showsBothExit bool
	}{
		{landingconfig.Route(""), true, true},
		{landingconfig.RouteDefault, true, true},
		{landingconfig.RouteManual, true, false},
		{landingconfig.RouteAutomated, false, false},
		{landingconfig.RouteCustom, false, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.route), func(t *testing.T) {
			if got := tt.route.OffersMerge(); got != tt.offersMerge {
				t.Errorf("OffersMerge = %v, want %v", got, tt.offersMerge)
			}
			if got := tt.route.ShowsBothExits(); got != tt.showsBothExit {
				t.Errorf("ShowsBothExits = %v, want %v", got, tt.showsBothExit)
			}
		})
	}
}

// The zero value is the default route, so a contract built without a route
// still advises the way an undeclared repository always has.
func TestZeroValueRouteIsTheDefault(t *testing.T) {
	var r landingconfig.Route
	if r.Effective() != landingconfig.RouteDefault {
		t.Errorf("Effective() = %q, want %q", r.Effective(), landingconfig.RouteDefault)
	}
	if !r.OffersMerge() || !r.ShowsBothExits() {
		t.Error("the zero value should behave as the default route")
	}
}
