package main

import (
	"os"
	"testing"
)

func TestRunRequiresExplicitManualReviewAndEnablement(t *testing.T) {
	if err := run(t.Context(), nil, func(string) string { return "" }, os.Stdout); err == nil {
		t.Fatal("missing manual confirmation accepted")
	}
	if err := run(t.Context(), []string{"run", "--confirmed-manual-review"}, func(string) string { return "" }, os.Stdout); err == nil {
		t.Fatal("disabled manual retention accepted")
	}
}
