{{/* Copyright (c) 2026 Mesh Intelligence Inc. */}}
{{/* SPDX-License-Identifier: MIT-0 */}}
{{/*
A provider library fragment's endpoint and its allowlist are one source: its
allow_scheme, allow_host, and allow_port arguments (srd058 R1.6). They default
to the library's own endpoint, and the fragment reads no environment, so where
this release puts the provider somewhere else the chart renders the three as
literals from the same values the pod's other clients read (srd061 R5.2).
*/}}

{{/* The allow arguments naming one URL, in flow form: its scheme, hostname,
     and port, defaulting the port by scheme when the URL names none. */}}
{{- define "chatbot-mesh.embedAllowArgs" -}}
{{- $url := urlParse . -}}
{{- $port := ternary "443" "80" (eq $url.scheme "https") -}}
{{- $parts := splitList ":" $url.host -}}
{{- if gt (len $parts) 1 }}{{ $port = last $parts }}{{ end -}}
allow_scheme: {{ $url.scheme }}, allow_host: {{ $url.hostname }}, allow_port: {{ $port }}
{{- end -}}

{{/* The Cohere endpoint the values declare, as the allowlist pair names it
     (srd009 R1.2): the fragment's allow arguments are that pair. */}}
{{- define "chatbot-mesh.cohereAllowArgs" -}}
allow_scheme: {{ .Values.cohere.scheme }}, allow_host: {{ .Values.cohere.host }}, allow_port: {{ .Values.cohere.port }}
{{- end -}}

{{/* The library the corpus-ingest child's embedder root binds (srd058 R2.2):
     the configured provider, else Cohere when the release enables it, else
     empty, which leaves the profile's Ollama library. The creator passes it
     as CORPUS_INGEST_EMBEDDER and the embed-rest rewrite reads the same. */}}
{{- define "chatbot-mesh.ingestEmbedder" -}}
{{- $embedder := .Values.controlPlane.creator.ingestEmbedding.provider -}}
{{- if and (not $embedder) .Values.cohere.enabled -}}
{{- $embedder = "cohere" -}}
{{- end -}}
{{- $embedder -}}
{{- end -}}

{{/* The chatbot's query embedding allow arguments: Cohere's endpoint on the
     Cohere tier, the LLM URL the pod takes as OLLAMA_URL on the Ollama tier. */}}
{{- define "chatbot-mesh.chatbotEmbedAllowArgs" -}}
{{- if .Values.cohere.enabled -}}
{{ include "chatbot-mesh.cohereAllowArgs" . }}
{{- else -}}
{{ include "chatbot-mesh.embedAllowArgs" (include "chatbot-mesh.llmURL" .) }}
{{- end -}}
{{- end -}}

{{/* The corpus-ingest child's document embedding allow arguments. An Ollama
     embedder with no ingestOllamaURL renders none: the child then reaches the
     library default, the loopback Ollama it reached when it had no OLLAMA_URL. */}}
{{- define "chatbot-mesh.ingestEmbedAllowArgs" -}}
{{- if eq (include "chatbot-mesh.ingestEmbedder" .) "cohere" -}}
{{ include "chatbot-mesh.cohereAllowArgs" . }}
{{- else if .Values.controlPlane.creator.ingestOllamaURL -}}
{{ include "chatbot-mesh.embedAllowArgs" .Values.controlPlane.creator.ingestOllamaURL }}
{{- end -}}
{{- end -}}

{{/* The request turn's rerank and the harvest's document embedding, from the
     Cohere library (agents/chatbot/provider-rest.yaml). Only the Cohere tier
     runs them, and the load refuses an expansion no declared word uses
     (srd052 R3.1), so the Ollama tier renders the file with none. */}}
{{- define "chatbot-mesh.chatbotProviderRest" -}}
kind: rest-definition
schema_version: 1
unit: mesh-chatbot-provider-rest
{{- if .Values.cohere.enabled }}
expand:
  - fragment: /opt/cohere/rerank-fragment.yaml
    args: {query_selector: $from(declare_question).body.question, documents_selector: $from(parse_candidates).body.documents, body_source: command_state, model: {{ .Values.cohere.rerankModel | quote }}, top_n: {{ .Values.cohere.rerankTopN }}, {{ include "chatbot-mesh.cohereAllowArgs" . }}}
  - fragment: /opt/cohere/embed-document-fragment.yaml
    args: {input_selector: $from(harvest_result).body.row.snippet, id_selector: $from(harvest_result).body.row.link, body_source: command_state, model: {{ .Values.cohere.embedModel | quote }}, {{ include "chatbot-mesh.cohereAllowArgs" . }}}
{{- end }}
rest:
  version: v1
{{- end -}}
