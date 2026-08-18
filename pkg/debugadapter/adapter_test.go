package debugadapter

import (
	"reflect"
	"testing"
)

func TestGoAdapterPlansSingleTestDeterministically(t *testing.T) {
	plan, err := NewRegistry().Plan("Go", Request{
		Action:     "debug",
		ProjectDir: "services/api",
		Target: Target{
			Name: "TestHTTP_200", Kind: "test", Language: "Go",
			Path: "services/api/http_test.go", Directory: "services/api", Line: 18, Column: 6,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ProjectDir != "services/api" || plan.Configuration["mode"] != "test" || plan.Configuration["program"] != "." {
		t.Fatalf("plan = %#v", plan)
	}
	wantArgs := []string{"-test.run", `^TestHTTP_200$`}
	if !reflect.DeepEqual(plan.Configuration["args"], wantArgs) {
		t.Fatalf("args = %#v, want %#v", plan.Configuration["args"], wantArgs)
	}
	if len(plan.Breakpoints) != 1 || plan.Breakpoints[0].Line != 18 {
		t.Fatalf("breakpoints = %#v", plan.Breakpoints)
	}
}

func TestPythonAdapterPlansWorkspaceRelativeScript(t *testing.T) {
	plan, err := NewRegistry().Plan("Python", Request{
		Action:     "run",
		ProjectDir: "services/api",
		Target: Target{
			Name: "main.py", Kind: "script", Language: "Python",
			Path: "services/api/main.py", Directory: "services/api", Line: 1, Column: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Configuration["program"] != "main.py" || plan.Configuration["noDebug"] != true || len(plan.Breakpoints) != 0 {
		t.Fatalf("plan = %#v", plan)
	}
}
