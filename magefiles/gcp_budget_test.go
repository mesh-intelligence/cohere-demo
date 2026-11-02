// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"errors"
	"strings"
	"testing"
)

// budgetRecorder answers the budget calls: the billing link, the listing, and
// success for everything else.
func budgetRecorder(calls *[]string, listing string) gcpCommandRunner {
	return func(name string, args ...string) ([]byte, error) {
		call := name + " " + strings.Join(args, " ")
		*calls = append(*calls, call)
		switch {
		case strings.Contains(call, "billing projects describe"):
			return []byte("billingAccounts/AAAAAA-BBBBBB-CCCCCC\n"), nil
		case strings.Contains(call, "billing accounts describe"):
			return []byte("CAD\n"), nil
		case strings.Contains(call, "billing budgets list"):
			return []byte(listing), nil
		}
		return nil, nil
	}
}

func TestEnsureGcpBudgetCreatesTheProjectBudget(t *testing.T) {
	var calls []string
	budget, err := ensureGcpBudget(budgetRecorder(&calls, "WARNING: nothing listed\n[]"), "p", "", 100)
	if err != nil || budget.Outcome != "created" || budget.Account != "AAAAAA-BBBBBB-CCCCCC" {
		t.Fatalf("ensureGcpBudget = %+v, %v", budget, err)
	}
	if !strings.HasPrefix(calls[0], "gcloud services enable billingbudgets.googleapis.com --project p") {
		t.Errorf("the API is not enabled first: %q", calls[0])
	}
	create := calls[len(calls)-1]
	for _, want := range []string{
		"billing budgets create", "--billing-account=AAAAAA-BBBBBB-CCCCCC", "--display-name=cohere-demo",
		"--budget-amount=100CAD", "--filter-projects=projects/p",
		"--threshold-rule=percent=0.5", "--threshold-rule=percent=0.9", "--threshold-rule=percent=1.0",
	} {
		if !strings.Contains(create, want) {
			t.Errorf("the create call lacks %s: %s", want, create)
		}
	}
}

// A budget that exists is updated in place, so a second run never adds a
// second budget, and a named billing account skips the lookup.
func TestEnsureGcpBudgetUpdatesTheExistingBudget(t *testing.T) {
	var calls []string
	listing := `[{"name":"billingAccounts/X/budgets/b1","displayName":"cohere-demo"},` +
		`{"name":"billingAccounts/X/budgets/b2","displayName":"other"}]`
	budget, err := ensureGcpBudget(budgetRecorder(&calls, listing), "p", "X", 150)
	if err != nil || budget.Outcome != "updated" || budget.Amount != "150CAD" {
		t.Fatalf("ensureGcpBudget = %+v, %v", budget, err)
	}
	for _, call := range calls {
		if strings.Contains(call, "budgets create") || strings.Contains(call, "projects describe") {
			t.Errorf("unexpected call %q", call)
		}
	}
	update := calls[len(calls)-1]
	for _, want := range []string{
		"billing budgets update billingAccounts/X/budgets/b1", "--budget-amount=150CAD",
		"--clear-threshold-rules", "--add-threshold-rule=percent=0.9",
	} {
		if !strings.Contains(update, want) {
			t.Errorf("the update call lacks %s: %s", want, update)
		}
	}
}

func TestEnsureGcpBudgetRefusesAmbiguity(t *testing.T) {
	var calls []string
	listing := `[{"name":"a","displayName":"cohere-demo"},{"name":"b","displayName":"cohere-demo"}]`
	if _, err := ensureGcpBudget(budgetRecorder(&calls, listing), "p", "X", 100); err == nil ||
		!strings.Contains(err.Error(), "2 budgets named cohere-demo") {
		t.Errorf("two same-named budgets = %v", err)
	}
	if _, err := ensureGcpBudget(budgetRecorder(&calls, "[]"), "p", "X", 0); err == nil {
		t.Error("a zero budget was accepted")
	}
	unlinked := func(name string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "projects describe") {
			return []byte("\n"), nil
		}
		return nil, nil
	}
	if _, err := ensureGcpBudget(unlinked, "p", "", 100); err == nil ||
		!strings.Contains(err.Error(), "no billing account") {
		t.Errorf("an unlinked project = %v", err)
	}
	failing := func(string, ...string) ([]byte, error) { return []byte("PERMISSION_DENIED"), errors.New("exit 1") }
	if _, err := ensureGcpBudget(failing, "p", "X", 100); err == nil || !strings.Contains(err.Error(), "PERMISSION_DENIED") {
		t.Errorf("a failed enable = %v", err)
	}
}

func TestGcpSettingsDefaultTheBudget(t *testing.T) {
	settings, err := loadGcpSettings(gcpTestRoot(t, "project: p\n"))
	if err != nil {
		t.Fatal(err)
	}
	if settings.BudgetAmount != 100 || settings.BillingAccount != "" {
		t.Errorf("defaults = %d %q", settings.BudgetAmount, settings.BillingAccount)
	}
	named, err := loadGcpSettings(gcpTestRoot(t, "project: p\nbilling_account: X\nbudget_amount: 40\n"))
	if err != nil || named.BudgetAmount != 40 || named.BillingAccount != "X" {
		t.Errorf("named = %+v, %v", named, err)
	}
}
