{{/* Chart name. Not overridable: values.schema.json closes the root with
     additionalProperties false and declares no nameOverride, so any values file
     setting one is refused before a template runs. The branch that read it could
     never be reached (GH-220). */}}
{{- define "chatbot-mesh.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Fully qualified release name. */}}
{{- define "chatbot-mesh.fullname" -}}
{{- printf "%s-%s" .Release.Name (include "chatbot-mesh.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Common labels. */}}
{{- define "chatbot-mesh.labels" -}}
app.kubernetes.io/name: {{ include "chatbot-mesh.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{/* Selector labels for a component. */}}
{{- define "chatbot-mesh.selectorLabels" -}}
app.kubernetes.io/name: {{ include "chatbot-mesh.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* Component resource name: <fullname>-<component>. */}}
{{- define "chatbot-mesh.component" -}}
{{- printf "%s-%s" (include "chatbot-mesh.fullname" .root) .name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* The agent runtime image reference. */}}
{{- define "chatbot-mesh.image" -}}
{{- printf "%s:%s" .Values.image.repository .Values.image.tag -}}
{{- end -}}

{{/* The pod-spec imagePullSecrets for the private runtime image; empty when none are set. */}}
{{- define "chatbot-mesh.imagePullSecrets" -}}
{{- with .Values.imagePullSecrets }}
imagePullSecrets:
{{- toYaml . | nindent 2 }}
{{- end }}
{{- end -}}

{{/* The LLM base URL: in-cluster Ollama Service or the external endpoint. */}}
{{- define "chatbot-mesh.llmURL" -}}
{{- if .Values.ollama.enabled -}}
http://{{ include "chatbot-mesh.fullname" . }}-ollama:{{ .Values.llm.port }}
{{- else -}}
{{ .Values.llm.externalURL }}
{{- end -}}
{{- end -}}

{{/*
The full list of models the LLM tier must hold: the embedding model, every chat
model, and the tier-selector model, named once in values (GH-337). The preload Job pulls
these and the agent readiness probe checks for them, so a model cannot be preloaded
but unrendered or gated-on but unpulled.
*/}}
{{- define "chatbot-mesh.ollamaModels" -}}
{{- $models := list .Values.ollama.models.embedding .Values.ollama.models.tier -}}
{{- range .Values.ollama.models.chat -}}
{{- $models = append $models . -}}
{{- end -}}
{{- $models | uniq | join " " -}}
{{- end -}}

{{/*
The LLM-tier readiness init container (srd015 R6.3): an agent pod blocks in Init
until every declared model is present in the in-cluster Ollama /api/tags, so a
missing model is a deploy-time gate rather than a runtime turn failure. Rendered
only when the in-cluster tier is enabled; an external endpoint is the operator's to
have ready. busybox supplies wget and grep.
*/}}
{{- define "chatbot-mesh.llmReadyInit" -}}
{{- if .Values.ollama.enabled }}
- name: wait-for-llm-models
  image: busybox:1.36
  command: ["/bin/sh", "-c"]
  args:
    - |
      set -eu
      url="http://{{ include "chatbot-mesh.fullname" . }}-ollama:{{ .Values.llm.port }}/api/tags"
      for m in {{ include "chatbot-mesh.ollamaModels" . }}; do
        base="${m%%:*}"
        until wget -qO- "$url" 2>/dev/null | grep -q "$base"; do
          echo "waiting for model $m at $url..."; sleep 5
        done
      done
      echo "LLM tier ready: all models present"
{{- end }}
{{- end -}}

{{/* The OTLP endpoint agents export to: the collector, else empty. */}}
{{- define "chatbot-mesh.otlpEndpoint" -}}
{{- if .Values.collector.enabled -}}
{{ include "chatbot-mesh.fullname" . }}-collector:{{ .Values.collector.otlpGRPCPort }}
{{- end -}}
{{- end -}}

{{/*
In production agent mode the declarative collector owns both trace and OTLP
metric intake (GH-1207). Integration overlays may send agent metrics directly to
the persistent external collector because the declarative collector does not
relay its metric spool. Traces continue through the local collector and its
declared relay. The contrib collector-metrics gateway remains retired (GH-1366).
*/}}
{{- define "chatbot-mesh.otlpMetricEndpoint" -}}
{{- if and .Values.collector.enabled (eq .Values.collector.implementation "agent") -}}
{{- if .Values.collector.externalOTLPEndpoint -}}
{{ .Values.collector.externalOTLPEndpoint }}
{{- else -}}
{{ include "chatbot-mesh.fullname" . }}-collector:{{ .Values.collector.otlpGRPCPort }}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
chatbot-mesh.embeddingModel is the one embedding identity a deployment ingests,
reports, and queries with. Takes a dict of "root" and "unit".

Three consumers read it and they must agree, because srd002 R3.3 excludes any
source whose reported identity differs from the query vector's: RAG_EMBEDDING_MODEL,
which the rag-server reports; CORPUS_EMBEDDING_MODEL, which the creator's family
gate compares an intent against and the corpus child embeds with; and the
co-generated topology.

The Cohere tier decides it when enabled, because that is what the corpus child
actually embeds with -- the chart binds its embedder root to the Cohere library
(srd058 R2.2). Reading ragUnits[].embeddingModel there
instead left the rag-servers reporting one family while the corpus was written
in another, so every source dropped out of every turn, and the creator's gate
refused the family it ingests at while admitting the one it does not (GH-213).

Per-unit values still decide with the tier off. They cannot vary per unit with
it on, and could not usefully before either: one turn embeds one query vector
under one identity, so a unit reporting anything else is excluded whatever it
holds.
*/}}
{{- define "chatbot-mesh.embeddingModel" -}}
{{- if .root.Values.cohere.enabled -}}
{{- .root.Values.cohere.embedModel -}}
{{- else -}}
{{- .unit.embeddingModel -}}
{{- end -}}
{{- end -}}

{{/*
chatbot-mesh.cogeneratedProfileKeys names the profile ConfigMap keys rendered
from values rather than packaged from disk, as a space-separated list.

One definition, because the two readers disagreed and the disagreement was
invisible: the ConfigMap excluded these keys from its file glob so the rendered
versions win, while the volume projection listed a different set -- naming two
files that are packaged verbatim and omitting the topology, which is actually
co-generated. The topology key reached the pod only because a packaging step had
left the staged file on disk for the glob to find (GH-220).
*/}}
{{- define "chatbot-mesh.cogeneratedProfileKeys" -}}
agents__chatbot__rest.yaml agents__chatbot__monitor-rest.yaml agents__chatbot__provider-rest.yaml applications__chatbot-mesh__observer__monitor-rest.yaml agents__chatbot__ui__ui.yaml agents__chatbot__request-topology-declarations.yaml agents__chatbot__request-machine.yaml agents__chatbot__request-tools.yaml agents__chatbot__request-declarations.yaml agents__chatbot__request-osint-declarations.yaml
{{- end -}}

{{/*
chatbot-mesh.renderedProfileKeys names the keys the profiles ConfigMap serves
from a rewritten packaged file rather than verbatim: the applier's exec
declarations, whose placeholder coordinates become the installed release's
(GH-484), the chatbot's query embedding, whose model and endpoint follow the
tier (srd002 R3.3, srd058 R1.6), and the corpus-ingest document embedding,
whose endpoint follows the child's embedder. They are packaged files, so they
are not co-generated, and they are not the file's own bytes, so they cannot
travel out of release with the verbatim profiles (GH-785). Both readers of that split -- this chart and the
deployment tooling that decides what to externalize -- read this list.
*/}}
{{- define "chatbot-mesh.renderedProfileKeys" -}}
applications__chatbot-mesh__applier__exec-declarations.yaml agents__chatbot__embed-rest.yaml agents__corpus-ingest__embed-rest.yaml
{{- end -}}

{{- define "chatbot-mesh.integrationResourceAttributes" -}}
{{- if .Values.collector.externalOTLPEndpoint -}}
test.repository={{ .Values.collector.integrationResource.repository }},test.module={{ .Values.collector.integrationResource.module }},test.target={{ .Values.collector.integrationResource.target }},vcs.ref.head.revision={{ .Values.collector.integrationResource.commit }},test.run.id={{ .Values.collector.integrationResource.runID }}
{{- end -}}
{{- end -}}


{{/*
The MySQL-wire DSN to the Dolt sql-server checkpoint backend (agent-core
srd035/srd036), or empty when Dolt is disabled. The chatbot persists checkpoints
here for explicit history, rollback, and resume operations. A Deployment rollout
drains active HTTP turns; persistence cannot reattach an existing client socket.
*/}}
{{- define "chatbot-mesh.doltDSN" -}}
{{- if .Values.dolt.enabled -}}
{{ .Values.dolt.user }}@tcp({{ include "chatbot-mesh.fullname" . }}-dolt:{{ .Values.dolt.port }})/{{ .Values.dolt.database }}
{{- end -}}
{{- end -}}

{{/*
The profiles volume, scoped to the groups a workload genuinely reads (GH-311).
Called with (dict "root" $ "groups" (list "<agent>" ...)): the volume projects
one ConfigMap source per named group -- an agent root under profiles/agents/,
or "applications" for the applications subtree -- with each "__"-encoded key
restored to its nested path, so the agent sees the original tree at the mount.
The membership logic mirrors profiles-configmap.yaml, and the completeness
check in TestStagedProfilesFitTheConfigMapLimit holds the two together.
GH-314 co-generates the chatbot rest.yaml into this subtree before packaging.
*/}}
{{- define "chatbot-mesh.profilesVolume" -}}
{{- $root := .root -}}
- name: profiles
  projected:
    defaultMode: 0444
    sources:
    {{- $cogen := splitList " " (include "chatbot-mesh.cogeneratedProfileKeys" $root) }}
    {{- range $group := .groups }}
    {{- /* The verbatim profiles travel out of release when the deployment
          provisioned them (GH-785): the group's own ConfigMap then carries the
          co-generated keys alone, and this second source carries the rest. The
          key names the file, so the path is the same "__" -> "/" rule either
          way and a pod sees one tree. */}}
    {{- $external := index (($root.Values.profiles.external | default dict)) $group | default dict }}
    {{- with $external.configMap }}
    - configMap:
        name: {{ . }}
        items:
        {{- range $external.keys }}
          - {key: {{ . }}, path: {{ . | replace "__" "/" }}}
        {{- end }}
    {{- end }}
    {{- /* The in-release half: this group's keys that the release itself
          decides. Accumulated first because the source is only named when it
          has items -- an externalized group whose keys are all verbatim renders
          no ConfigMap, and a projected source naming an absent object leaves
          every pod mounting it in PodInitializing. */}}
    {{- $keep := include "chatbot-mesh.renderedProfileKeys" $root | splitList " " }}
    {{- $items := list }}
    {{- range $path, $_ := $root.Files.Glob "profiles/**" }}
      {{- $rel := $path | trimPrefix "profiles/" }}
      {{- $member := false }}
      {{- if eq $group "applications" }}
      {{- $member = hasPrefix "applications/" $rel }}
      {{- else }}
      {{- $member = hasPrefix (printf "agents/%s/" $group) $rel }}
      {{- end }}
      {{- $key := $rel | replace "/" "__" }}
      {{- /* Served UI bundles are projected from their own ConfigMaps by the
            agents that serve them (GH-131). A rewritten key stays in release
            whether or not the verbatim profiles left it (GH-785): the pod must
            read the render, not the packaged bytes. */}}
      {{- if and $member (not (has $key $cogen)) (not (hasPrefix "agents__chatbot__ui__app__dist__" $key))
                 (or (not $external.configMap) (has $key $keep)) }}
      {{- $items = append $items $key }}
      {{- end }}
    {{- end }}
    {{- /* Each group's own co-generated keys, projected whether or not a
          packaging step placed the file on disk. A key under applications__
          belongs to the applications group, as the wrapper profiles do; every
          other key names its owning agent (GH-737). */}}
    {{- range $key := $cogen }}
    {{- $owner := "applications" }}
    {{- if not (hasPrefix "applications__" $key) }}
    {{- $owner = index (splitList "__" $key) 1 }}
    {{- end }}
    {{- if eq $owner $group }}
    {{- $items = append $items $key }}
    {{- end }}
    {{- end }}
    {{- if $items }}
    - configMap:
        name: {{ include "chatbot-mesh.fullname" $root }}-profiles-{{ $group }}
        items:
        {{- range $items }}
          - {key: {{ . }}, path: {{ . | replace "__" "/" }}}
        {{- end }}
    {{- end }}
    {{- end }}
{{- end -}}

{{/*
A rollout checksum over the named profile groups, so a workload rolls when its
own mounted profiles change and stays put when another agent's do (GH-311).
Hashes the same content the group ConfigMaps carry: the member files plus, for
the chatbot group, the co-generated renders (which fold the values they read
into the hash).
*/}}
{{- define "chatbot-mesh.profilesChecksum" -}}
{{- $root := .root -}}
{{- $content := "" -}}
{{- range $group := .groups }}
{{- /* A group provisioned outside the release contributes the checksum the
      deployment measured over its content (GH-785). The glob below no longer
      sees those files, so without this the annotation would hash the shrinking
      remainder and a profile edit would not roll the workload that reads it. */}}
{{- $external := index (($root.Values.profiles.external | default dict)) $group | default dict }}
{{- with $external.checksum }}
{{- $content = printf "%s%s" $content . }}
{{- end }}
{{- range $path, $_ := $root.Files.Glob "profiles/**" }}
{{- $rel := $path | trimPrefix "profiles/" }}
{{- $member := false }}
{{- if eq $group "applications" }}
{{- $member = hasPrefix "applications/" $rel }}
{{- else }}
{{- $member = hasPrefix (printf "agents/%s/" $group) $rel }}
{{- end }}
{{- if $member }}
{{- $content = printf "%s%s:%s" $content $rel ($root.Files.Get $path | sha256sum) }}
{{- end }}
{{- end }}
{{- if eq $group "chatbot" }}
{{- $content = printf "%s%s" $content (include "chatbot-mesh.chatbotRest" $root | sha256sum) }}
{{- $content = printf "%s%s" $content (include "chatbot-mesh.chatbotUI" $root | sha256sum) }}
{{- $content = printf "%s%s" $content (include "chatbot-mesh.chatbotTopology" $root | sha256sum) }}
{{- end }}
{{- if eq $group "applications" }}
{{- $content = printf "%s%s" $content (include "chatbot-mesh.observerMonitorRest" $root | sha256sum) }}
{{- end }}
{{- end }}
{{- $content | sha256sum -}}
{{- end -}}

{{/*
chatbot-mesh.chatProviderArgs rebinds the chatbot's providers library root to the
Ollama library when the Cohere provider is off (srd058 R2.2). The profiles bind
it to Cohere; the tier-switched invoke_llm words read their chat dialect from it,
so this flag, not a rewrite of the declarations, is what moves them. The flag is
process-scoped, so the chat endpoint's request profile follows it too.
*/}}
{{- define "chatbot-mesh.chatProviderArgs" -}}
{{- if not .Values.cohere.enabled -}}
- "--library"
- "providers=/opt/agent-core/tools/providers/ollama"
{{- end -}}
{{- end }}
