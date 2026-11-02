{{/* Copyright (c) 2026 Mesh Intelligence Inc. */}}
{{/* SPDX-License-Identifier: MIT-0 */}}
{{/*
The observer's monitor-rest.yaml, rendered from the packaged file with one
values-driven substitution (srd003 R2). Everything else in that file already
follows the deployment through the OBSERVER_* environment references the chart
sets -- the bind address, the monitor port, and every monitor_proxy upstream --
so only the fleet UI's monitored-agents list has to be co-generated: it is a
list whose length comes from .Values.ragUnits, and an environment reference
cannot carry one.

The list and the proxy upstreams name the same agents, so the served UI names
exactly what the proxy reaches (srd004 R2.1, R9.2). A substitution that stops
matching its anchor renders the packaged default, which names rag0 alone; the
helmSmoke observer assertions read the served ui-config.json against the
release's own units and fail when that happens.
*/}}
{{- define "chatbot-mesh.observerMonitorRest" -}}
{{- $packaged := $.Files.Get "profiles/applications/chatbot-mesh/observer/monitor-rest.yaml" -}}
{{- $anchor := "              monitored_agents:\n                - {name: chatbot, label: Chatbot}\n                - {name: rag0, label: RAG server}\n" -}}
{{- $rendered := "              monitored_agents:\n                - {name: chatbot, label: Chatbot}\n" -}}
{{- range $i, $unit := .Values.ragUnits }}
{{- $rendered = printf "%s                - {name: %s, label: RAG server %d}\n" $rendered $unit.name $i }}
{{- end }}
{{- $packaged | replace $anchor $rendered -}}
{{- end -}}
