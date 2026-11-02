// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"path/filepath"
)

// cohereRerankClient and cohereRerankOperation are the rerank the Cohere
// provider library declares, which the chatbot's provider-rest.yaml
// instantiates for all three cascade passes (srd058 R1.3, srd011 R4.4).
const (
	cohereRerankClient    = "reranker"
	cohereRerankOperation = "rerank"
)

// cohereRerankPath is the operation rel10.2 declares.
const cohereRerankPath = "/v2/rerank"

// assertCohereRerankOperationPinned confirms the rerank pass reaches the
// provider through a declared operation whose authority is fixed rather than
// supplied at runtime (srd009 R1.1, R1.5; rel10.2-uc001 S5).
//
// The live target proves the rerank's behaviour -- that a buried document is
// lifted, that the reordered set is a permutation, that a refusal degrades --
// and cannot prove any of this, because a declaration that named a runtime
// authority would still rerank correctly against the host it was pointed at.
// Reading the declaration unexpanded is what separates the two, and it needs
// neither credential nor network.
func assertCohereRerankOperationPinned(client restClientDeclaration) error {
	operation, ok := client.Operations[cohereRerankOperation]
	if !ok {
		return fmt.Errorf("the %s client declares no %s operation (srd009 R1.1)",
			cohereRerankClient, cohereRerankOperation)
	}
	if operation.Method != "POST" || operation.Path != cohereRerankPath {
		return fmt.Errorf("cohere %s is %s %s, want POST %s (srd009 R1.1, R1.5)",
			cohereRerankOperation, operation.Method, operation.Path, cohereRerankPath)
	}
	// The rag client declares base_url_source because its authority comes from
	// the trusted topology. This one must not: the candidates carry ministry
	// document text, and a runtime-supplied authority could send them to a host
	// the operator never configured.
	if operation.BaseURLSource != "" {
		return fmt.Errorf("cohere %s declares base_url_source %q; the Cohere authority is"+
			" fixed configuration (srd009 R1.5)", cohereRerankOperation, operation.BaseURLSource)
	}
	return nil
}

// cohereRerankerDeclaration reads the Cohere provider library's rerank client,
// the one the chatbot's provider-rest.yaml instantiates, unexpanded.
func cohereRerankerDeclaration(coreRoot string) (restClientDeclaration, error) {
	var cfg struct {
		Rest struct {
			Clients map[string]restClientDeclaration `yaml:"clients"`
		} `yaml:"rest"`
	}
	path := filepath.Join(coreRoot, "tools", "providers", "cohere", "rerank-fragment.yaml")
	if err := readIntegrationYAML(path, "cohere rerank fragment", &cfg); err != nil {
		return restClientDeclaration{}, err
	}
	client, ok := cfg.Rest.Clients[cohereRerankClient]
	if !ok {
		return restClientDeclaration{}, fmt.Errorf("%s declares no %s client (srd009 R1.1)", path, cohereRerankClient)
	}
	return client, nil
}
