// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// The rel12.1 tracer (rel12.1-uc001, srd011 R3-R4). It proves the cascade
// against the deployed mesh: a brief composed from the fused cross-source
// top-N and citing both branches, a search-rerank failure that leaves engine
// order and still fuses, a fusion failure that falls back to the reranked
// RAG branch, and the single-fusion collapse running with both per-branch
// passes skipped.
//
// Set membership alone is not evidence (suite case 1): the pass statuses in
// metadata say which ordering path actually ran, and the static pins tie
// each status to its construction -- fusion applied means the composed set
// is the reordered union trimmed to the declared budget, because the fused
// compose reads the reorder and nothing else. The tracer asserts the
// statuses and the cross-source citations together.
//
// The degradation legs steer the deployment the way the rel12.0 tracer
// does: full-data profile-key rewrites under helm's field manager, restored
// on every path. Each steer swaps one order check's satisfied/unsatisfied
// signals or the cascade-mode operand -- the exact bytes a rendered
// deployment could carry -- so the failure is deterministic rather than
// waited for.

const (
	cascadeSearchCheckEnabled  = "      satisfied: OsintRerankOrderEmpty\n      unsatisfied: OsintRerankOrderPresent"
	cascadeSearchCheckInverted = "      satisfied: OsintRerankOrderPresent\n      unsatisfied: OsintRerankOrderEmpty"
	cascadeFusedCheckEnabled   = "      satisfied: FusedRerankOrderEmpty\n      unsatisfied: FusedRerankOrderPresent"
	cascadeFusedCheckInverted  = "      satisfied: FusedRerankOrderPresent\n      unsatisfied: FusedRerankOrderEmpty"
	cascadeThreePassOperand    = "left: cascade-three-pass"
	cascadeSingleFusionOperand = "left: cascade-single-fusion"
)

type cascadeTurn struct {
	Answer   string `json:"answer"`
	Metadata struct {
		AnsweredBy string `json:"answered_by"`
		Rerank     string `json:"rerank"`
		Fusion     string `json:"fusion"`
		Osint      struct {
			Status string `json:"status"`
			Rerank string `json:"rerank"`
		} `json:"osint"`
		Citations []struct {
			RecordID string `json:"record_id"`
		} `json:"citations"`
	} `json:"metadata"`
}

// runCascadeTurn drives one brief turn, retrying past the transient errors a
// fresh rollout serves, and decodes the cascade-relevant metadata.
func runCascadeTurn(base string) (cascadeTurn, error) {
	body, err := json.Marshal(map[string]interface{}{"message": osintAugmentTasking, "history": []interface{}{}})
	if err != nil {
		return cascadeTurn{}, err
	}
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		var turn cascadeTurn
		request, err := http.NewRequest(http.MethodPost, base+conformanceChatEndpoint, bytes.NewReader(body))
		if err != nil {
			return turn, err
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Analyst-Clearance", "secret")
		client := &http.Client{Timeout: conformanceTurnTimeout}
		response, err := client.Do(request)
		if err == nil && response.StatusCode == http.StatusOK {
			err = json.NewDecoder(response.Body).Decode(&turn)
			_ = response.Body.Close()
			if err == nil {
				return turn, nil
			}
			lastErr = err
		} else if err == nil {
			_ = response.Body.Close()
			lastErr = fmt.Errorf("chat returned status %d", response.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(20 * time.Second)
	}
	return cascadeTurn{}, lastErr
}

func (turn cascadeTurn) citationSplit() (internal, external int) {
	for _, citation := range turn.Metadata.Citations {
		if citation.RecordID == osintFixtureRecordURL {
			external++
		} else if citation.RecordID != "" {
			internal++
		}
	}
	return internal, external
}

// cascadeSteerAndTurn rewrites the osint declarations key, rolls the
// chatbot, and drives one turn against the steered shape.
func cascadeSteerAndTurn(base, steered string) (cascadeTurn, error) {
	if err := osintPatchKeys(map[string]string{osintDeclarationsKey: steered}); err != nil {
		return cascadeTurn{}, err
	}
	if err := osintRollChatbot(); err != nil {
		return cascadeTurn{}, err
	}
	return runCascadeTurn(base)
}

// CascadeRerank proves the rel12.1 cascade release against the deployed
// mesh: the fused leg, the search-degradation leg, the fusion-fallback leg,
// and the single-fusion collapse. It skips with the cause named when the
// mesh is absent or answers on the pre-provider tier, which carries no
// rerank cascade -- deploy with COHERE_API_KEY exported (GH-318).
func (Integration) CascadeRerank() error {
	base := envOrDefault("CHATBOT_UI_BASE", chatbotUIDefaultBase)
	if err := chatbotUIReachable(base); err != nil {
		fmt.Printf("SKIP integration:cascadeRerank: %s does not serve the mesh (%v);"+
			" run mage app:up and mage app:seed first\n", base, err)
		return nil
	}
	declKey, err := osintConfigMapKey(osintDeclarationsKey)
	if err != nil {
		return err
	}
	for _, want := range []string{cascadeSearchCheckEnabled, cascadeFusedCheckEnabled, cascadeThreePassOperand} {
		if !strings.Contains(declKey, want) {
			return fmt.Errorf("the deployed declarations do not carry the default three-pass cascade;" +
				" redeploy with mage app:up before running the tracer")
		}
	}

	// The fused leg (suite cases 1-4's live half): all three passes applied
	// on one turn, and the brief cites both branches -- documents of the
	// internal corpus and the external fixture in one composed set, which
	// only the fused path produces. Paced retries absorb provider throttling;
	// a mesh that never answers on cohere has no cascade to prove, and the
	// skip names the credential (GH-318).
	fmt.Println("integration:cascadeRerank: fused leg")
	var fused bool
	for attempt := 1; attempt <= 4; attempt++ {
		if attempt > 1 {
			time.Sleep(osintPace)
		}
		turn, err := runCascadeTurn(base)
		if err != nil {
			return err
		}
		if turn.Metadata.AnsweredBy != "cohere" {
			if attempt == 1 && turn.Metadata.Rerank == "" {
				fmt.Println("SKIP integration:cascadeRerank: the mesh answers on the pre-provider tier," +
					" which carries no rerank cascade; export COHERE_API_KEY and redeploy (GH-318)")
				return nil
			}
			fmt.Printf("integration:cascadeRerank: attempt %d answered_by %s; pacing and retrying\n",
				attempt, turn.Metadata.AnsweredBy)
			continue
		}
		if turn.Metadata.Rerank != "applied" || turn.Metadata.Osint.Rerank != "applied" || turn.Metadata.Fusion != "applied" {
			// A throttled provider degrades a single pass on an otherwise
			// healthy turn; that is the pacing's job, not a verdict.
			fmt.Printf("integration:cascadeRerank: attempt %d pass statuses rag=%s search=%s fusion=%s; pacing and retrying\n",
				attempt, turn.Metadata.Rerank, turn.Metadata.Osint.Rerank, turn.Metadata.Fusion)
			continue
		}
		internal, external := turn.citationSplit()
		if internal == 0 || external == 0 {
			fmt.Printf("integration:cascadeRerank: attempt %d cited internal=%d external=%d; pacing and retrying\n",
				attempt, internal, external)
			continue
		}
		fmt.Printf("integration:cascadeRerank: fused on attempt %d -- all three passes applied, %d internal and %d external citations\n",
			attempt, internal, external)
		fused = true
		break
	}
	if !fused {
		return fmt.Errorf("fused leg: no turn cited both branches in 4 paced attempts with every pass applied;" +
			" composition holds and citing is what missed")
	}

	restore := func() error {
		if err := osintPatchKeys(map[string]string{osintDeclarationsKey: declKey}); err != nil {
			return err
		}
		return osintRollChatbot()
	}
	failRestored := func(cause error) error {
		if restoreErr := restore(); restoreErr != nil {
			return fmt.Errorf("%w (and restoring the deployment failed: %v)", cause, restoreErr)
		}
		return cause
	}

	// The search-degradation leg (suite case 9): the search pass's order
	// check inverted routes every ordering to the empty leg, the branch
	// composes in engine order with the degradation recorded, and the turn
	// continues into the fusion.
	fmt.Println("integration:cascadeRerank: search-degradation leg")
	turn, err := cascadeSteerAndTurn(base,
		strings.Replace(declKey, cascadeSearchCheckEnabled, cascadeSearchCheckInverted, 1))
	if err != nil {
		return failRestored(err)
	}
	if turn.Metadata.Osint.Rerank != "degraded" {
		return failRestored(fmt.Errorf("search-degradation leg: search pass status %q, want degraded", turn.Metadata.Osint.Rerank))
	}
	if turn.Metadata.Fusion != "applied" {
		return failRestored(fmt.Errorf("search-degradation leg: fusion %q; a degraded branch must still fuse (srd011 R3.4)", turn.Metadata.Fusion))
	}
	if strings.TrimSpace(turn.Answer) == "" {
		return failRestored(fmt.Errorf("search-degradation leg: the turn did not answer"))
	}
	fmt.Println("integration:cascadeRerank: search pass degraded to engine order and the turn fused")

	// The fusion-fallback leg (suite case 5): the fusion's order check
	// inverted routes every ordering to the empty leg, and the composed set
	// falls back to the reranked RAG branch -- fusion degraded, no external
	// citation, the turn healthy.
	fmt.Println("integration:cascadeRerank: fusion-fallback leg")
	turn, err = cascadeSteerAndTurn(base,
		strings.Replace(declKey, cascadeFusedCheckEnabled, cascadeFusedCheckInverted, 1))
	if err != nil {
		return failRestored(err)
	}
	if turn.Metadata.Fusion != "degraded" {
		return failRestored(fmt.Errorf("fusion-fallback leg: fusion status %q, want degraded", turn.Metadata.Fusion))
	}
	if _, external := turn.citationSplit(); external != 0 {
		return failRestored(fmt.Errorf("fusion-fallback leg: the fallback still cited the external document; the composed set must be the reranked RAG branch (srd011 R4.5)"))
	}
	if turn.Metadata.Rerank != "applied" || strings.TrimSpace(turn.Answer) == "" {
		return failRestored(fmt.Errorf("fusion-fallback leg: the reranked RAG branch did not compose (rag=%q)", turn.Metadata.Rerank))
	}
	fmt.Println("integration:cascadeRerank: fusion fell back to the reranked RAG branch")

	// The single-fusion leg (suite case 10): the cascade operand rewritten to
	// the collapse, both per-branch passes skipped, and the one fusion still
	// applied over the raw union.
	fmt.Println("integration:cascadeRerank: single-fusion leg")
	turn, err = cascadeSteerAndTurn(base,
		strings.Replace(declKey, cascadeThreePassOperand, cascadeSingleFusionOperand, 1))
	if err != nil {
		return failRestored(err)
	}
	if turn.Metadata.Rerank != "skipped" || turn.Metadata.Osint.Rerank != "skipped" {
		return failRestored(fmt.Errorf("single-fusion leg: pass statuses rag=%q search=%q, want both skipped", turn.Metadata.Rerank, turn.Metadata.Osint.Rerank))
	}
	if turn.Metadata.Fusion != "applied" {
		return failRestored(fmt.Errorf("single-fusion leg: fusion %q; the collapse still fuses on one scale (srd011 R6.4)", turn.Metadata.Fusion))
	}
	fmt.Println("integration:cascadeRerank: collapse skipped both passes and fused the raw union")

	if err := restore(); err != nil {
		return err
	}
	restored, err := runCascadeTurn(base)
	if err != nil {
		return err
	}
	if restored.Metadata.Fusion != "applied" || restored.Metadata.Rerank != "applied" {
		return fmt.Errorf("restore check: pass statuses rag=%q fusion=%q after restoring the deployment",
			restored.Metadata.Rerank, restored.Metadata.Fusion)
	}

	fmt.Println("integration:cascadeRerank PASS - a live brief composes from the fused cross-source top-N citing" +
		" both branches with every pass applied; a degraded search pass keeps engine order and still fuses; a" +
		" failed fusion falls back to the reranked RAG branch; and the single-fusion collapse skips both" +
		" per-branch passes while the union still fuses on one scale")
	return nil
}
