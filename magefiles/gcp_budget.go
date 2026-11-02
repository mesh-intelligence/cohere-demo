// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The project's spending ceiling (#881): one budget on the billing account,
// scoped to the project, alerting the billing administrators at half, nine
// tenths, and all of the monthly figure. The figure is in the billing
// account's currency, because the Budgets API rejects any other. A budget
// alerts and never stops a resource; gcp:down is what ends the spend.

const (
	gcpBudgetName          = "cohere-demo"
	gcpDefaultBudgetAmount = 100
	gcpBudgetAPI           = "billingbudgets.googleapis.com"
)

// gcpBudgetThresholds are the fractions of actual spend that alert.
var gcpBudgetThresholds = []string{"0.5", "0.9", "1.0"}

// Budget enables the Billing Budgets API and creates the cohere-demo budget on
// the project's billing account, or updates the existing one to the figure
// gcp.yaml names (budget_amount, in the account's currency, default 100). It
// needs Billing Account Administrator on that account.
func (Gcp) Budget() error {
	root, err := cohereApplicationRoot()
	if err != nil {
		return err
	}
	gcpOpenPhaseLog(root, "gcp:budget")
	settings, err := loadGcpSettings(root)
	if err != nil {
		return err
	}
	if err := gcpPreflight(gcpDefaultRun, settings.rigConfig(settings.defaultCluster())); err != nil {
		return err
	}
	started := time.Now()
	budget, err := ensureGcpBudget(gcpDefaultRun, settings.project(), settings.BillingAccount, settings.BudgetAmount)
	if err != nil {
		return gcpLogOutcome(settings.project(), "budget", "", started, err)
	}
	gcpLogPhase(settings.project(), "budget", budget.Outcome, started,
		fmt.Sprintf("account=%s amount=%s thresholds=%s", budget.Account, budget.Amount,
			strings.Join(gcpBudgetThresholds, ",")))
	return nil
}

// gcpBudget is what ensureGcpBudget converged: the account, the amount with
// its currency, and whether the budget was created or updated.
type gcpBudget struct {
	Account string
	Amount  string
	Outcome string
}

// ensureGcpBudget converges the one budget named gcpBudgetName.
func ensureGcpBudget(run gcpCommandRunner, project, account string, amount int) (gcpBudget, error) {
	if amount <= 0 {
		return gcpBudget{}, fmt.Errorf("budget_amount must be a positive whole number, got %d", amount)
	}
	if out, err := run("gcloud", "services", "enable", gcpBudgetAPI, "--project", project); err != nil {
		return gcpBudget{}, fmt.Errorf("enable %s: %w: %s", gcpBudgetAPI, err, strings.TrimSpace(string(out)))
	}
	if account == "" {
		var err error
		if account, err = gcpBillingAccount(run, project); err != nil {
			return gcpBudget{}, err
		}
	}
	currency, err := gcpBillingCurrency(run, account)
	if err != nil {
		return gcpBudget{}, err
	}
	existing, err := gcpExistingBudget(run, project, account)
	if err != nil {
		return gcpBudget{}, err
	}
	budget := gcpBudget{Account: account, Amount: fmt.Sprintf("%d%s", amount, currency), Outcome: "created"}
	args := []string{"billing", "budgets"}
	if existing == "" {
		args = append(args, "create", "--billing-account="+account, "--display-name="+gcpBudgetName,
			"--budget-amount="+budget.Amount, "--filter-projects=projects/"+project)
		args = append(args, gcpThresholdArgs("--threshold-rule")...)
	} else {
		args = append(args, "update", existing, "--billing-account="+account, "--budget-amount="+budget.Amount,
			"--clear-threshold-rules")
		args = append(args, gcpThresholdArgs("--add-threshold-rule")...)
		budget.Outcome = "updated"
	}
	args = append(args, "--billing-project", project)
	if out, err := run("gcloud", args...); err != nil {
		return gcpBudget{}, fmt.Errorf("budget %s not %s: %w: %s", gcpBudgetName, budget.Outcome,
			err, strings.TrimSpace(string(out)))
	}
	return budget, nil
}

func gcpThresholdArgs(flag string) []string {
	args := make([]string, 0, len(gcpBudgetThresholds))
	for _, percent := range gcpBudgetThresholds {
		args = append(args, flag+"=percent="+percent)
	}
	return args
}

// gcpBillingAccount reads the account the project bills to.
func gcpBillingAccount(run gcpCommandRunner, project string) (string, error) {
	out, err := run("gcloud", "billing", "projects", "describe", project, "--format=value(billingAccountName)")
	if err != nil {
		return "", fmt.Errorf("read the billing account of %s: %w: %s", project, err, strings.TrimSpace(string(out)))
	}
	account := strings.TrimPrefix(strings.TrimSpace(string(out)), "billingAccounts/")
	if account == "" {
		return "", fmt.Errorf("project %s has no billing account; link one, or set billing_account in %s",
			project, gcpConfigFile)
	}
	return account, nil
}

// gcpBillingCurrency reads the currency the account bills in.
func gcpBillingCurrency(run gcpCommandRunner, account string) (string, error) {
	out, err := run("gcloud", "billing", "accounts", "describe", account, "--format=value(currencyCode)")
	currency := strings.TrimSpace(string(out))
	if err != nil || len(currency) != 3 {
		return "", fmt.Errorf("read the currency of billing account %s: %v: %s", account, err, currency)
	}
	return currency, nil
}

// gcpExistingBudget returns the resource name of the budget already named
// gcpBudgetName, or "" when there is none. Two with that name is a state to
// report, not one to pick from.
func gcpExistingBudget(run gcpCommandRunner, project, account string) (string, error) {
	out, err := run("gcloud", "billing", "budgets", "list", "--billing-account="+account,
		"--billing-project", project, "--format=json")
	if err != nil {
		return "", fmt.Errorf("list the budgets of %s: %w: %s", account, err, strings.TrimSpace(string(out)))
	}
	var budgets []struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
	}
	// gcloud writes warnings ahead of the listing on the combined stream.
	if start := strings.IndexByte(string(out), '['); start > 0 {
		out = out[start:]
	}
	if err := json.Unmarshal(out, &budgets); err != nil {
		return "", fmt.Errorf("decode the budgets of %s: %w", account, err)
	}
	var named []string
	for _, budget := range budgets {
		if budget.DisplayName == gcpBudgetName {
			named = append(named, budget.Name)
		}
	}
	switch len(named) {
	case 0:
		return "", nil
	case 1:
		return named[0], nil
	}
	return "", fmt.Errorf("billing account %s has %d budgets named %s (%s); delete all but one",
		account, len(named), gcpBudgetName, strings.Join(named, ", "))
}
