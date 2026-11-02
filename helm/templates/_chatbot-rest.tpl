{{/*
The chatbot rest.yaml, co-generated from .Values.ragUnits (srd015 R2). One
selected-target RAG operation serves every declared topology item; the provider
egress allowlist and monitor_proxy upstreams derive from the same list that
renders the RAG objects, so selected authority and deployment cannot drift. The
packaged agents/chatbot/rest.yaml stays the local integration source; this render
overrides that ConfigMap key in the cluster. Server addresses bind 0.0.0.0 so the
Services route to the pod. Runtime {{`{{ params.x }}`}} bodies are emitted
literally.
*/}}
{{- define "chatbot-mesh.chatbotRest" -}}
{{- $fullname := include "chatbot-mesh.fullname" . -}}
{{- /* Request headers the deployment's edge adds to every request the chat
       port receives (ingress.gke.transportHeaders, set by the GKE overlay).
       The REST server refuses a header a route does not declare, so each
       public route declares them; they are accepted, never trusted. kind
       declares none (#883). */ -}}
{{- $transportHeaders := .Values.ingress.gke.transportHeaders | default list -}}
{{- $q := .Values.ragServer.ports.query -}}
{{- $mon := .Values.ragServer.ports.monitor -}}
{{- $llmURL := include "chatbot-mesh.llmURL" . -}}
{{- $llmHost := (urlParse $llmURL).hostname -}}
{{- $embedModel := .Values.chatbot.embeddingModel -}}
{{- $firstRag := first .Values.ragUnits -}}
{{/* The OSINT search endpoint (srd011 R1.2): the values-supplied engine URL,
     or the in-cluster fixture when none is set. The client's base_url is the
     origin and the operation path is the URL's path (the fixture's
     /search.json by default). The network allowlist is osint.allowedHosts
     when the deployment declares one, and otherwise derives from the same
     parse as the base_url so the approved host and the address dialed cannot
     drift; an endpoint outside a declared allowlist is dropped by the client
     (R1.3). A disabled branch (srd011 R6.2) renders the loopback address
     instead, so no external host appears anywhere in the policy and the
     capability is removed rather than merely skipped -- the client itself
     must stay declared, because the profile registers the search word at
     load whatever the toggle says. */}}
{{- $osintRaw := printf "http://127.0.0.1:%d/search.json" (int .Values.osint.port) -}}
{{- if .Values.osint.enabled -}}
{{- $osintRaw = .Values.osint.searchBaseURL | default (printf "http://%s-osint-fixture:%d/search.json" $fullname (int .Values.osint.port)) -}}
{{- end -}}
{{- $osintParsed := urlParse $osintRaw -}}
{{- $osintScheme := $osintParsed.scheme | default "http" -}}
{{- $osintHostport := splitList ":" $osintParsed.host -}}
{{- $osintHost := first $osintHostport -}}
{{- $osintPort := ternary 443 80 (eq $osintScheme "https") -}}
{{- if gt (len $osintHostport) 1 -}}{{- $osintPort = last $osintHostport | int -}}{{- end -}}
{{- $osintURL := printf "%s://%s" $osintScheme $osintParsed.host -}}
{{- $osintPath := $osintParsed.path -}}
kind: rest-definition
schema_version: 1
rest:
  version: v1
  auth:
    none:
      type: none
{{- if .Values.cohere.enabled }}
    cohere_bearer:
      type: bearer
      token_ref: COHERE_API_KEY
{{- if eq .Values.osint.engine "serpapi" }}
    # The SerpAPI credential rides the query string because that is the engine's only auth surface.
    serpapi_key:
      type: query_token
      query: api_key
      token_ref: SERPAPI_API_KEY
{{- end }}
{{- end }}
  limits:
{{- if .Values.cohere.enabled }}
    cohere_provider:
      # 60s, far under the 200s request budget, so a stalled provider hands the turn back while the invoke_llm_fast fallback (max_time 120) still fits: 60 + 120 = 180 < 200 (GH-225).
      timeout: 60s
      read_timeout: 60s
      max_request_bytes: 1048576
      max_response_bytes: 4194304
      redirect:
        mode: none
      network:
        schemes: [{{ .Values.cohere.scheme }}]
        hosts: [{{ .Values.cohere.host }}]
        ports: [{{ .Values.cohere.port }}]
    cohere_agentic:
      # A revision's supplementary search legs (GH-611): 20s, so a stalled provider gives the turn back and it composes over what it already holds.
      timeout: 20s
      read_timeout: 20s
      max_request_bytes: 1048576
      max_response_bytes: 4194304
      redirect:
        mode: none
      network:
        schemes: [{{ .Values.cohere.scheme }}]
        hosts: [{{ .Values.cohere.host }}]
        ports: [{{ .Values.cohere.port }}]
{{- end }}
    local_provider:
      timeout: 130s
      read_timeout: 130s
      max_request_bytes: 1048576
      max_response_bytes: 1048576
      redirect:
        mode: none
      network:
        schemes: [http]
        hosts:
          - 127.0.0.1
          - localhost
          - {{ $llmHost }}
{{- range .Values.ragUnits }}
          - {{ $fullname }}-{{ .name }}
{{- end }}
        ports: [{{ .Values.llm.port }}, {{ $q }}]
    # The read tier's own budget, apart from the provider budget the query rides: a document read answers in milliseconds and must not hold a browser request open on a provider timeout, while the envelope listing the facet vocabulary reads carries every document's full text and needs the wider response bound.
    local_rag_reads:
      timeout: 15s
      read_timeout: 15s
      max_request_bytes: 1048576
      max_response_bytes: 4194304
      redirect:
        mode: none
      network:
        schemes: [http]
        hosts:
          - 127.0.0.1
          - localhost
{{- range .Values.ragUnits }}
          - {{ $fullname }}-{{ .name }}
{{- end }}
        ports: [{{ $q }}]
    # The OSINT search branch's egress budget (srd011 R1.2, R5.2).
    osint_search:
{{- if eq .Values.osint.engine "serpapi" }}
      # The live engine runs a real search per call and routinely needs more
      # than the fixture's 15s; 45s stays far inside the turn budget (GH-457).
      timeout: 45s
      read_timeout: 45s
{{- else }}
      timeout: 15s
      read_timeout: 15s
{{- end }}
      max_request_bytes: 16384
      max_response_bytes: 1048576
      redirect:
        mode: none
      network:
{{- if eq .Values.osint.engine "serpapi" }}
        # The live engine's allowlist (GH-457): the one approved host, the
        # same derived-from-one-value rule the fixture leg keeps below.
        schemes: [https]
        hosts: [serpapi.com]
        ports: [443]
{{- else }}
        schemes: [{{ $osintScheme }}]
{{- if and .Values.osint.enabled .Values.osint.allowedHosts }}
        hosts:
{{- range .Values.osint.allowedHosts }}
          - {{ . }}
{{- end }}
{{- else }}
        hosts: [{{ $osintHost }}]
{{- end }}
        ports: [{{ $osintPort }}]
{{- end }}
{{- if .Values.exporter.enabled }}
    exporter_render:
      # Above the exporter endpoint's own 120s bound (srd013 R5.4) and under
      # the chat listener's 200s.
      timeout: 130s
      read_timeout: 130s
      max_request_bytes: 1048576
      max_response_bytes: 8388608
      redirect:
        mode: none
      network:
        schemes: [http]
        hosts: [{{ $fullname }}-exporter]
        ports: [{{ .Values.exporter.ports.export }}]
{{- end }}
    # The self search (GH-611): a revision pass runs its proposed query through
    # the chatbot's own declared /search surface on loopback. The ceiling bounds
    # one embed under the provider's 60s plus the fan's reads; a search past it
    # costs its pass and nothing else.
    local_self_search:
      timeout: 60s
      read_timeout: 60s
      max_request_bytes: 65536
      max_response_bytes: 4194304
      redirect:
        mode: none
      network:
        schemes: [http]
        hosts:
          - 127.0.0.1
        ports:
          - {{ .Values.chatbot.ports.chat }}
    local_chat_requests:
      # Matches the 290s chat endpoint bound (GH-225, GH-611): the listener's
      # limits must not close a request its endpoint still allows.
      timeout: 290s
      read_timeout: 290s
      max_request_bytes: 1048576
      # Sized by the largest response this listener serves: the envelope listing behind the facet vocabulary merges every unit's read of every document's full text, about 2 MB on the committed corpus plus the join bookkeeping, and each 1 MiB bound between the store and the browser turned that read into an unmapped 502 in turn.
      max_response_bytes: 4194304
      redirect:
        mode: none
      network:
        schemes: [http]
        hosts: [127.0.0.1, localhost]
        ports: [{{ .Values.chatbot.ports.chat }}]
        allow_public_listener: true
    local_chatbot_control:
      timeout: 30s
      read_timeout: 5s
      max_request_bytes: 16384
      max_response_bytes: 65536
      redirect:
        mode: none
      network:
        schemes: [http]
        hosts: [127.0.0.1, localhost]
        ports: [{{ .Values.chatbot.ports.control }}]
        allow_public_listener: true

  clients:
{{- if .Values.exporter.enabled }}
    # The export relay's one client (srd013 R4.1, GH-352): the SPA's export action reaches the exporter through this agent's origin, because the exporter's NetworkPolicy admits only chatbot-labelled callers.
    exporter:
      base_url: http://{{ $fullname }}-exporter:{{ .Values.exporter.ports.export }}
      auth_ref: none
      limits_ref: exporter_render
      operations:
        render_pdf:
          method: POST
          path: /export/api/pdf
          params:
            body_schema:
              type: object
              required: [schema_version, content]
              properties:
                schema_version: {type: string}
                content: {type: string}
            body_source: previous_result
            input_mapping:
              schema_version: $.schema_version
              content: $.content
          body:
            schema_version: "{{`{{ params.schema_version }}`}}"
            content: "{{`{{ params.content }}`}}"
          success: {status: [200], signal: ExportRendered}
          failures:
            - {status: [500], signal: ExportFailed}
          response:
            output:
              status: $.status
              pdf_base64: $.pdf_base64
          side_effects:
            - kind: external_api
              target: exporter.render_pdf
              state: request_scoped_render
          reversibility:
            classification: reversible
            undo: noop
{{- end }}
    # The read client is separate from the query client so the two carry different limits over the same tier: a retrieval query waits on the provider budget, a document read does not (srd016 R5.2).
    rag_documents:
      base_url: http://{{ $fullname }}-{{ $firstRag.name }}:{{ $q }}
      auth_ref: none
      limits_ref: local_rag_reads
      operations:
        documents:
          method: POST
          path: /api/v1/rag/documents
          base_url_source: command_state
          base_url_selector: $from(rag_unit).body.base_url
          params:
            body_schema:
              type: object
              required: [record_ids, max_clearance]
              properties:
                record_ids: {type: array}
                max_clearance: {type: string}
            body_source: command_state
            input_mapping:
              record_ids: $from(declare_read_request).body.record_ids
              # The same selector the query binds, so the read path and the retrieval path take the level from one place and nothing in the request can reach it (srd010 R3.2, R3.3).
              max_clearance: $from(analyst_clearance).body.level
          body:
            record_ids: "{{`{{ params.record_ids }}`}}"
            max_clearance: "{{`{{ params.max_clearance }}`}}"
          success: {status: [200], signal: DocumentsRead}
          failures:
            - {status: [400], signal: ReadRejected}
            - {status: [403], signal: ClearanceRefused}
          response:
            output:
              ids: $.ids
              documents: $.documents
              metadatas: $.metadatas
              clearance_enforced_at: $.clearance_enforced_at
          side_effects:
            - kind: external_api
              target: rag_server.documents
              state: read_only
          reversibility:
            classification: reversible
            undo: noop
        documents_query:
          method: POST
          path: /api/v1/rag/documents/query
          base_url_source: command_state
          base_url_selector: $from(rag_unit).body.base_url
          params:
            body_schema:
              type: object
              required: [document_filter, max_clearance]
              properties:
                document_filter: {type: object}
                max_clearance: {type: string}
            body_source: command_state
            input_mapping:
              document_filter: $from(declare_read_request).body.document_filter
              max_clearance: $from(analyst_clearance).body.level
          body:
            # The token stands alone so the map reaches the unit as JSON
            # rather than through Go's default map formatting.
            document_filter: "{{`{{ params.document_filter }}`}}"
            max_clearance: "{{`{{ params.max_clearance }}`}}"
          success: {status: [200], signal: DocumentsRead}
          failures:
            - {status: [400], signal: ReadRejected}
            - {status: [403], signal: ClearanceRefused}
          response:
            output:
              ids: $.ids
              documents: $.documents
              metadatas: $.metadatas
              clearance_enforced_at: $.clearance_enforced_at
          side_effects:
            - kind: external_api
              target: rag_server.documents
              state: read_only
          reversibility:
            classification: reversible
            undo: noop
    rag:
      # A configured fallback remains required by the REST client schema. The
      # operation selects each declared item's authority through command state.
      base_url: http://{{ $fullname }}-{{ $firstRag.name }}:{{ $q }}
      auth_ref: none
      limits_ref: local_provider
      operations:
        ingest:
          method: POST
          path: /api/v1/rag/ingest
          base_url_source: command_state
          base_url_selector: $from(rag_unit).body.base_url
          params:
            body_schema:
              type: object
              required: [rows]
              properties:
                rows: {type: array}
            body_source: command_state
            input_mapping:
              rows: $from(marshal_harvest_rows).body.rows
          body:
            rows: "{{`{{ params.rows }}`}}"
          success: {status: [200], signal: RowsIngested}
          failures:
            - {status: [500], signal: IngestFailed}
          response:
            output:
              ingested_ids: $.ingested_ids
          side_effects:
            - kind: external_api
              target: rag_server.ingest
              state: records_added
          # The relay's precedent is the export render: the remote effect belongs to the callee, whose own machine declares the write and its compensation.
          reversibility:
            classification: reversible
            undo: noop
        # The purge relay (GH-648): ask each declared unit to drop its harvested rows; the store owner declares the delete and its scope.
        purge_harvest:
          method: POST
          path: /api/v1/rag/purge-harvest
          base_url_source: command_state
          base_url_selector: $from(rag_unit).body.base_url
          params:
            body_schema: {type: object, properties: {}}
            body_source: command_state
            input_mapping: {}
          body:
            reason: ui-reset
          success: {status: [200], signal: UnitPurged}
          failures:
            - {status: [500], signal: UnitPurgeFailed}
          side_effects:
            - kind: external_api
              target: rag_server.purge
              state: records_deleted
          reversibility:
            classification: reversible
            undo: noop
        query:
          method: POST
          path: /api/v1/rag/query
          base_url_source: command_state
          base_url_selector: $from(rag_unit).body.base_url
          params:
            body_schema:
              type: object
              required: [query_embeddings, max_clearance]
              properties:
                query_embeddings: {type: array}
                max_clearance: {type: string}
            body_source: command_state
            input_mapping:
              query_embeddings: $from(parse_query_vector).body.embedding
              # One selector for every item, so the level binds uniformly with
              # no per-source variation (srd010 R3.2).
              max_clearance: $from(analyst_clearance).body.level
          body:
            query_embeddings: "{{`{{ params.query_embeddings }}`}}"
            n_results: {{ .Values.chatbot.retrievalBreadth }}
            max_clearance: "{{`{{ params.max_clearance }}`}}"
          success: {status: [200], signal: QueryResponded}
          failures:
            # 400 embedding-space mismatch -> QueryRejected (excluded, srd014 R3.3),
            # distinct from a degraded (CommandError) RAG.
            - {status: [400], signal: QueryRejected}
            # 403 clearance refusal -> its own signal: the turn degrades on it
            # (srd010 R4.3) while the response keeps it apart from a break (R5.1).
            - {status: [403], signal: ClearanceRefused}
          response:
            output:
              ids: $.ids
              documents: $.documents
              distances: $.distances
              # The chunks' declared metadata, projected into the composed source outcomes so the interface fills the source-evaluation table and the classification banner (srd016 R3.4).
              metadatas: $.metadatas
              embedding_model: $.embedding_model
              clearance_enforced_at: $.clearance_enforced_at
          side_effects:
            - kind: external_api
              target: rag_server.query
              state: read_only
          reversibility:
            classification: reversible
            undo: noop
        query_faceted:
          method: POST
          path: /api/v1/rag/query-faceted
          base_url_source: command_state
          base_url_selector: $from(rag_unit).body.base_url
          params:
            body_schema:
              type: object
              required: [query_embeddings, max_clearance, facet_where, keyword]
              properties:
                query_embeddings: {type: array}
                max_clearance: {type: string}
                facet_where: {type: object}
                keyword: {type: string}
            body_source: command_state
            input_mapping:
              query_embeddings: $from(parse_query_vector).body.embedding
              # One selector for every item, so the level binds uniformly with
              # no per-source variation (srd010 R3.2), exactly as on query.
              max_clearance: $from(analyst_clearance).body.level
              # The facets ride from the request body through the embed carry.
              # Both legs publish this label (GH-694): the analyst's facets when set, else the selection's proposed tiers rendered by the declared template.
              facet_where: $from(retrieval_facets).body.facet_where
              keyword: $from(retrieval_facets).body.keyword
          body:
            query_embeddings: "{{`{{ params.query_embeddings }}`}}"
            n_results: {{ .Values.chatbot.retrievalBreadth }}
            max_clearance: "{{`{{ params.max_clearance }}`}}"
            facet_where: "{{`{{ params.facet_where }}`}}"
            keyword: "{{`{{ params.keyword }}`}}"
          success: {status: [200], signal: QueryResponded}
          failures:
            # The same outcome mapping as query: a 400 excludes the source distinctly from a degraded one, and a 403 keeps a refusal apart from a failure (srd010 R4.2, R4.3, R5.1).
            - {status: [400], signal: QueryRejected}
            - {status: [403], signal: ClearanceRefused}
          response:
            output:
              ids: $.ids
              documents: $.documents
              distances: $.distances
              metadatas: $.metadatas
              embedding_model: $.embedding_model
              clearance_enforced_at: $.clearance_enforced_at
          side_effects:
            - kind: external_api
              target: rag_server.query
              state: read_only
          reversibility:
            classification: reversible
            undo: noop
    # The OSINT search client (srd011 R1, R2), rendered unconditionally: the profile registers the search word at load, and a registered word whose rest_ref is undefined is refused there.
    osint:
{{- if eq .Values.osint.engine "serpapi" }}
      # The live engine (GH-457): SerpAPI, credentialed by query_token and metered, so it is never the default -- fixture stays the shipped engine and this branch is a per-deploy opt-in.
      base_url: https://serpapi.com
      auth_ref: serpapi_key
{{- else }}
      base_url: {{ $osintURL }}
      auth_ref: none
{{- end }}
      limits_ref: osint_search
      operations:
        search:
          method: GET
          path: {{ $osintPath | default "/search.json" }}
          params:
            # The declared egress payload (srd011 R2.1-R2.4): the tasking, as the q parameter, and nothing else.
            query:
              q: ""
{{- if eq .Values.osint.engine "serpapi" }}
              engine: google
              num: {{ .Values.osint.serpapi.num | quote }}
{{- end }}
            body_source: command_state
            input_mapping:
              # The Command model's distilled query, or the raw question on the fallback and Ollama legs -- whichever wrote the shared label (GH-457).
              q: $from(search_terms).$
          success: {status: [200], signal: SearchResponded}
          response:
            output:
              # SerpAPI's response shape is the branch's one shape (GH-457);
              # the fixture serves the same rows.
              results: $.organic_results
          side_effects:
            - kind: external_api
              target: osint.search
              state: read_only
          reversibility:
            classification: reversible
            undo: noop
{{- if .Values.cohere.enabled }}
    cohere:
      base_url: {{ .Values.cohere.baseURL }}
      auth_ref: cohere_bearer
      limits_ref: cohere_provider
      operations:
        # Source selection as a native tool call (GH-607): the flagship model is forced to call the one declared tool, so the names array arrives schema-enforced.
        select_sources_tool:
          method: POST
          path: /v2/chat
          params:
            body_schema:
              type: object
              required: [prompt]
              properties:
                prompt: {type: string}
            body_source: command_state
            input_mapping:
              prompt: $from(wrap_source_selection_prompt).body.prompt
          body:
            model: {{ .Values.cohere.model }}
            messages:
              - role: system
                content: You select which declared RAG sources should be queried for a user's question. Call the select_sources tool exactly once, naming every source whose description directly covers the question. Use only names from the catalog; never add a source merely for general context. When the question names a collection discipline - HUMINT field reporting, OSINT assessments or news, SIGINT strike or loss records - also set source_types to those tiers; otherwise omit source_types so every tier is searched. When the question names a time period, also set date_from and date_to as YYYYMMDD integers bounding it and set source_types (all five tiers if no discipline is named); omit the dates otherwise.
              - role: user
                content: "{{`{{ params.prompt }}`}}"
            tools:
              - type: function
                function:
                  name: select_sources
                  description: Report which declared RAG sources to query for the question.
                  parameters:
                    type: object
                    properties:
                      names:
                        type: array
                        items: {type: string}
                        description: Declared source names to query.
                      source_types:
                        type: array
                        items: {type: string, enum: [humint, osint_assessment, osint_news, sigint_event, sigint_series]}
                        description: Collection tiers to narrow retrieval to; omit to search every tier.
                      date_from:
                        type: integer
                        minimum: 19000101
                        maximum: 21001231
                        description: Start of the requested reporting window as a YYYYMMDD integer; omit when no period is named.
                      date_to:
                        type: integer
                        minimum: 19000101
                        maximum: 21001231
                        description: End of the requested reporting window as a YYYYMMDD integer; omit when no period is named.
                    required: [names]
            tool_choice: REQUIRED
          success: {status: [200], signal: LLMResponded}
          failures:
            - {status: [401, 403], signal: ProviderUnauthorized}
            - {status: [429], signal: ProviderThrottled}
            - {status: [500, 502, 503, 504], signal: ProviderUnavailable}
          response:
            output:
              # Selector paths walk maps only, so the forced single tool call is captured as the whole array; project_tool_arguments unwraps it.
              tool_calls: $.message.tool_calls
          side_effects:
            - kind: external_api
              target: cohere.chat
              state: read_only
          reversibility:
            classification: reversible
            undo: noop


        # Web-search query proposals as a native tool call (GH-612): the flagship is forced to call the one declared tool, so the queries array arrives schema-enforced.
        # The segment translator (GH-670): the flagship returns one translation per segment, same order, via a forced tool call.
        translate_segments_tool:
          method: POST
          path: /v2/chat
          params:
            body_schema:
              type: object
              required: [prompt]
              properties:
                prompt: {type: string}
            body_source: command_state
            input_mapping:
              prompt: $from(compose_translate_prompt).$
          body:
            model: {{ .Values.cohere.model }}
            messages:
              - role: system
                content: Translate each segment of a military brief. Call deliver_translations exactly once with one translation per segment, same order and count. Keep markings, identifiers, numbers, and dates verbatim; never merge, split, drop, or add segments.
              - role: user
                content: "{{`{{ params.prompt }}`}}"
            tools:
              - type: function
                function:
                  name: deliver_translations
                  description: Deliver the translated segments, one per input segment, same order.
                  parameters:
                    type: object
                    properties:
                      translations:
                        type: array
                        items: {type: string}
                        description: One translation per input segment, same order and count.
                    required: [translations]
            tool_choice: REQUIRED
          success: {status: [200], signal: LLMResponded}
          failures:
            - {status: [401, 403], signal: ProviderUnauthorized}
            - {status: [429], signal: ProviderThrottled}
            - {status: [500, 502, 503, 504], signal: ProviderUnavailable}
          response:
            output:
              tool_calls: $.message.tool_calls
          side_effects:
            - kind: external_api
              target: cohere.chat
              state: read_only
          reversibility:
            classification: reversible
            undo: noop
        derive_search_terms_tool:
          method: POST
          path: /v2/chat
          params:
            body_schema:
              type: object
              required: [prompt]
              properties:
                prompt: {type: string}
            body_source: command_state
            input_mapping:
              prompt: $from(compose_search_terms_prompt).$
          body:
            model: {{ .Values.cohere.model }}
            messages:
              - role: system
                content: You turn analyst taskings into web search queries. Call the report_search_terms tool exactly once with one to three short keyword queries that together cover the tasking. When the tasking names more than one subject, propose one query per subject; never restate the whole tasking as a single query when it has several subjects, and never propose near-duplicates.
              - role: user
                content: "{{`{{ params.prompt }}`}}"
            tools:
              - type: function
                function:
                  name: report_search_terms
                  description: Report the web search queries the tasking implies.
                  parameters:
                    type: object
                    properties:
                      queries:
                        type: array
                        items: {type: string}
                        description: One to three short keyword queries.
                    required: [queries]
            tool_choice: REQUIRED
          success: {status: [200], signal: LLMResponded}
          failures:
            - {status: [401, 403], signal: ProviderUnauthorized}
            - {status: [429], signal: ProviderThrottled}
            - {status: [500, 502, 503, 504], signal: ProviderUnavailable}
          response:
            output:
              tool_calls: $.message.tool_calls
          side_effects:
            - kind: external_api
              target: cohere.chat
              state: read_only
          reversibility:
            classification: reversible
            undo: noop

        compose_brief:
          method: POST
          path: /v2/chat
          params:
            body_schema:
              type: object
              required: [message, documents]
              properties:
                message: {type: string}
                documents: {type: array}
            body_source: command_state
            input_mapping:
              # Through the chat_message label: the plain turn's declared question, or the revision turn's instruction fused with the prior draft (GH-446) -- whichever wrote the label last.
              message: $from(chat_message).body.question
              documents: $from(parse_documents).body.documents
          body:
            model: {{ .Values.cohere.model }}
            messages:
              - role: system
                content: |
                  {{- if .Values.chatbot.report.instruction }}
                  {{- .Values.chatbot.report.instruction | nindent 18 }}
                  {{- else }}
                  You compose a command-staff intelligence brief from the
                  documents supplied with the request.

                  Rules:
                  - Compose only from the supplied documents. Do not use outside
                    knowledge, and do not speculate beyond what they state.
                  - Where the documents do not answer the tasking, say so
                    plainly under the heading it belongs to.
                  - Structure the brief as markdown with the ministry headings:
                    Assessment, Key Judgements, Supporting Reporting, and Gaps.
                  - Attribute every assertion to the document it came from.
                  {{- end }}
              - role: user
                content: "{{`{{ params.message }}`}}"
            documents: "{{`{{ params.documents }}`}}"
            stream: false
          success: {status: [200], signal: BriefComposed}
          failures:
            - {status: [400, 422], signal: ProviderRejected}
            - {status: [401, 403], signal: ProviderUnauthorized}
            - {status: [429], signal: ProviderThrottled}
            - {status: [500, 502, 503, 504], signal: ProviderUnavailable}
          response:
            output:
              content: $.message.content
              citations: $.message.citations
              finish_reason: $.finish_reason
          side_effects:
            - kind: external_api
              target: cohere.chat
              state: read_only
          reversibility:
            classification: reversible
            undo: noop
    # The revision's agentic search (GH-611): the same provider, host, and
    cohere_agentic:
      base_url: {{ .Values.cohere.baseURL }}
      auth_ref: cohere_bearer
      limits_ref: cohere_agentic
      operations:
        # One forced tool call over the instruction and the prior brief; the
        propose_searches_tool:
          method: POST
          path: /v2/chat
          params:
            body_schema:
              type: object
              required: [prompt]
              properties:
                prompt: {type: string}
            body_source: command_state
            input_mapping:
              prompt: $from(compose_propose_searches_prompt).$
          body:
            model: {{ .Values.cohere.model }}
            messages:
              - role: system
                content: You plan retrieval for revising an intelligence brief. Call the propose_searches tool exactly once with one to three short queries for material the revision needs and the brief does not already hold -- what the instruction asks for, and what the brief refers to without the reporting behind it. Fill first, and second and third only when distinct subjects need their own query; never restate the instruction as a whole, never propose near-duplicates, and leave unused fields out.
              - role: user
                content: "{{`{{ params.prompt }}`}}"
            tools:
              - type: function
                function:
                  name: propose_searches
                  description: Propose the retrieval queries the revision needs.
                  # Named fields rather than an array: selectors path into maps
                  # only, so each pass reads its own field (GH-611).
                  parameters:
                    type: object
                    properties:
                      first: {type: string, description: The one query the revision most needs.}
                      second: {type: string, description: A second query for a distinct subject, when one exists.}
                      third: {type: string, description: A third query for a distinct subject, when one exists.}
                    required: [first]
            tool_choice: REQUIRED
          success: {status: [200], signal: LLMResponded}
          failures:
            - {status: [401, 403], signal: ProviderUnauthorized}
            - {status: [429], signal: ProviderThrottled}
            - {status: [500, 502, 503, 504], signal: ProviderUnavailable}
          response:
            output:
              tool_calls: $.message.tool_calls
          side_effects:
            - kind: external_api
              target: cohere.chat
              state: read_only
          reversibility:
            classification: reversible
            undo: noop
{{- end }}
    chatbot_self:
      # The chatbot's own chat server on loopback (GH-611): the agentic passes
      # read the mesh's declared /search surface rather than restating its
      # embed-and-fan chain. The request machine serving /search runs in its own
      # request scope, so a turn searching itself does not contend with itself.
      base_url: http://127.0.0.1:{{ .Values.chatbot.ports.chat }}
      auth_ref: none
      limits_ref: local_self_search
      operations:
        search_proposed:
          method: POST
          path: /api/v1/search
          params:
            headers:
              # The requester's clearance, forwarded verbatim so the search sees
              # exactly the caller's level (srd010 R3.2).
              x-analyst-clearance: {type: string}
            body_schema:
              type: object
              required: [message]
              properties:
                message: {type: string}
            body_source: command_state
            input_mapping:
              message: $from(proposal_query).$
              # The same label the turn's own reads bind their level from.
              x-analyst-clearance: $from(analyst_clearance).body.level
          body:
            message: "{{`{{ params.message }}`}}"
          success: {status: [200], signal: ProposalSearched}
          response:
            output:
              results: $.results
          side_effects:
            - kind: external_api
              target: chatbot.search
              state: read_only
          reversibility:
            classification: reversible
            undo: noop
  servers:
    chatbot_chat:
      address: 0.0.0.0:{{ .Values.chatbot.ports.chat }}
      limits_ref: local_chat_requests
      # A rollout asks the host machine to stop through the lifecycle endpoint.
      shutdown:
        # Endpoint bound plus drain headroom (GH-812); moves with the 200s below.
        timeout: 205s
        drain_policy: drain_then_stop
      endpoints:
        chat:
          method: POST
          path: /api/v1/chat
          binding: machine_request
          request:
            body_schema:
              type: object
              required: [message]
              properties:
                message: {type: string}
                history: {type: array}
                # The retrieval facets, additive to the request contract (srd016 R4.4); the committed rest.yaml carries the full shape argument.
                facets:
                  type: object
                  required: [facet_where, keyword]
                  properties:
                    facet_where: {type: object}
                    keyword: {type: string}
                reader_clearance_rank: {type: integer, minimum: 0, maximum: 3}
                # A revision turn's retained prior draft (GH-446); presence is
                # the predicate, and the typed message is the instruction.
                prior_brief: {}
                # The records the prior draft cited, read back by id at the
                # requester's clearance and joined to the instruction's
                # retrieval (GH-604); the committed rest.yaml carries the full
                # argument.
                prior_record_ids:
                  type: array
                  items: {type: string}
                  maxItems: 100
            # Declared so the request is accepted carrying it; an undeclared header is refused outright.
            headers:
              x-analyst-clearance: {type: string}
{{- include "chatbot-mesh.transportHeaderEntries" (dict "headers" $transportHeaders "indent" 14) }}
          machine_request:
            # The request-scoped profile, not the agent's own (GH-86).
            profile: request-profile.yaml
            machine: request-machine.yaml
            # One provider stall (60s) plus the fallback answer (120s) plus a
            # revision's bounded supplementary legs; the committed rest.yaml
            # carries the full argument (GH-225, GH-604, GH-611).
            timeout: 290s
            request:
              body:
                message: $.message
                history: $.history
                # A body field on purpose, unlike the clearance below: facets are constraints the client legitimately composes, conjoined under the clearance window at the retrieval tier (srd016 R4.3).
                facets: $.facets
                reader_clearance_rank: $.reader_clearance_rank
                prior_brief: $.prior_brief
                prior_record_ids: $.prior_record_ids
              # The analyst's maximum clearance comes from the authenticated request context, not from a body field a client composes (srd010 R3.1).
              headers:
                max_clearance: $.x-analyst-clearance
            response:
              terminal_states:
                LLMResponded:
                  status: 200
                  content_type: application/json
                  body:
                    answer: $.answer
                    metadata: $.metadata
                Failed:
                  status: 500
                  content_type: application/json
                  body:
                    error: command_error
                    message: $.message
{{- if .Values.exporter.enabled }}
        # The analyst's export action (srd013 R4.1, R4.2, GH-352): relayed to
        # the exporter because its NetworkPolicy admits only this agent.
{{- if $.Values.cohere.enabled }}
        # The /search command (GH-447): retrieval alone, the embedding model isolated.
        search:
          method: POST
          path: /api/v1/search
          binding: machine_request
          request:
            body_schema:
              type: object
              required: [message]
              properties:
                message: {type: string}
            headers:
              x-analyst-clearance: {type: string}
{{- include "chatbot-mesh.transportHeaderEntries" (dict "headers" $transportHeaders "indent" 14) }}
          machine_request:
            profile: request-profile.yaml
            machine: search-machine.yaml
            # One embed under the provider's 60s ceiling plus a sequential
            # fan-out whose per-unit read authority is 2m10s.
            timeout: 200s
            request:
              body:
                message: $.message
              headers:
                max_clearance: $.x-analyst-clearance
            response:
              terminal_states:
                SearchServed:
                  status: 200
                  content_type: application/json
                  body:
                    results: $.results
                    query: $.query
                    clearance_enforced_at: $.clearance_enforced_at
                    failed_sources: $.failed_sources
                Failed:
                  status: 500
                  content_type: application/json
                  body:
                    error: command_error
                    message: $.message
        # The /harvest command (GH-459): search, embed, and ingest into the scratch corpus through the canonical pipeline.
        harvest:
          method: POST
          path: /api/v1/harvest
          binding: machine_request
          request:
            body_schema:
              type: object
              required: [message]
              properties:
                message: {type: string}
{{- with $transportHeaders }}
            headers:
{{- include "chatbot-mesh.transportHeaderEntries" (dict "headers" . "indent" 14) }}
{{- end }}
          machine_request:
            profile: request-profile.yaml
            machine: harvest-machine.yaml
            # One search under the engine authority plus one embed per result
            # and the unit relays; under the machine's own 3m budget.
            timeout: 200s
            request:
              body:
                message: $.message
            response:
              terminal_states:
                HarvestServed:
                  status: 200
                  content_type: application/json
                  body:
                    harvested: $.harvested
                    failed_embeds: $.failed_embeds
                    queries: $.queries
                Failed:
                  status: 500
                  content_type: application/json
                  body:
                    error: command_error
                    message: $.message
        # The reset purge (GH-648): relay to every declared unit's purge surface; the store owner scopes the delete to its own web-harvest marker.
        purge:
          method: POST
          path: /api/v1/harvest/purge
          binding: machine_request
          request:
            body_schema: {type: object, properties: {reason: {type: string}}}
{{- with $transportHeaders }}
            headers:
{{- include "chatbot-mesh.transportHeaderEntries" (dict "headers" . "indent" 14) }}
{{- end }}
          machine_request:
            profile: request-profile.yaml
            machine: harvest-purge-machine.yaml
            timeout: 60s
            request:
              body:
                reason: $.reason
            response:
              terminal_states:
                PurgeServed:
                  status: 200
                  content_type: application/json
                  body:
                    purged: $.purged
                Failed:
                  status: 500
                  content_type: application/json
                  body:
                    error: command_error
                    message: $.message
        # The report language selector (GH-670): the approved brief's sentence segments translated as a rendition; the English record stays authoritative.
        translate:
          method: POST
          path: /api/v1/translate
          binding: machine_request
          request:
            body_schema:
              type: object
              required: [segments, language]
              properties:
                segments: {type: array}
                language: {type: string}
{{- with $transportHeaders }}
            headers:
{{- include "chatbot-mesh.transportHeaderEntries" (dict "headers" . "indent" 14) }}
{{- end }}
          machine_request:
            profile: request-profile.yaml
            machine: translate-machine.yaml
            timeout: 90s
            request:
              body:
                segments: $.segments
                language: $.language
            response:
              terminal_states:
                TranslateServed:
                  status: 200
                  content_type: application/json
                  body:
                    translations: $.translations
                Failed:
                  status: 500
                  content_type: application/json
                  body:
                    error: command_error
                    message: $.message
        # The /audit command (GH-543): the classifier tier scores one cited claim against the chunk it cites.
        audit:
          method: POST
          path: /api/v1/audit
          binding: machine_request
          request:
            body_schema:
              type: object
              required: [summary, document]
              properties:
                summary: {type: string}
                document: {type: string}
{{- with $transportHeaders }}
            headers:
{{- include "chatbot-mesh.transportHeaderEntries" (dict "headers" . "indent" 14) }}
{{- end }}
          machine_request:
            profile: request-profile.yaml
            machine: audit-machine.yaml
            timeout: 60s
            request:
              body:
                summary: $.summary
                document: $.document
            response:
              terminal_states:
                AuditServed:
                  status: 200
                  content_type: application/json
                  body:
                    verdict: $.verdict
                Failed:
                  status: 500
                  content_type: application/json
                  body:
                    error: command_error
                    message: $.message
        # The /summarize command (GH-448): the Command model isolated over the document the window holds.
        summarize:
          method: POST
          path: /api/v1/summarize
          binding: machine_request
          request:
            body_schema:
              type: object
              required: [document]
              properties:
                document: {type: string}
{{- with $transportHeaders }}
            headers:
{{- include "chatbot-mesh.transportHeaderEntries" (dict "headers" . "indent" 14) }}
{{- end }}
          machine_request:
            profile: request-profile.yaml
            machine: summarize-machine.yaml
            timeout: 90s
            request:
              body:
                document: $.document
            response:
              terminal_states:
                SummaryServed:
                  status: 200
                  content_type: application/json
                  body:
                    summary: $.summary
                Failed:
                  status: 500
                  content_type: application/json
                  body:
                    error: command_error
                    message: $.message
{{- end }}
        export:
          method: POST
          path: /api/v1/export
          binding: machine_request
          request:
            body_schema:
              type: object
              required: [schema_version, content]
              properties:
                schema_version: {type: string}
                content: {type: string}
{{- with $transportHeaders }}
            headers:
{{- include "chatbot-mesh.transportHeaderEntries" (dict "headers" . "indent" 14) }}
{{- end }}
          machine_request:
            profile: request-profile.yaml
            machine: export-relay-machine.yaml
            timeout: 140s
            request:
              body:
                schema_version: $.schema_version
                content: $.content
            response:
              terminal_states:
                Rendered:
                  status: 200
                  content_type: application/json
                  body:
                    schema_version: "1"
                    status: rendered
                    # The invoke word nests its response mapping under
                    # mapped, the osint_search shape.
                    pdf_base64: $.mapped.pdf_base64
                RenderFailed:
                  status: 500
                  content_type: application/json
                  body:
                    schema_version: "1"
                    status: failed
                    error: export_failed
                Failed:
                  status: 502
                  content_type: application/json
                  body:
                    schema_version: "1"
                    status: failed
                    error: exporter_unreachable
{{- end }}
        monitor_proxy:
          method: GET
          path: /monitor-proxy/{agent}/{path...}
          binding: monitor_proxy
          monitor_proxy:
            upstreams:
              chatbot: http://127.0.0.1:{{ .Values.chatbot.ports.monitor }}
{{- range $unit := .Values.ragUnits }}
              {{ $unit.name }}: http://{{ $fullname }}-{{ $unit.name }}:{{ $mon }}
{{- end }}
{{- /* enabled as well as implementation: with the collector off, the
       implementation default still reads "agent", and gating on it alone
       declared a monitor-proxy upstream at a Service the chart never renders.
       chatbot-mesh.otlpEndpoint checks both (GH-220). */}}
{{- if and .Values.collector.enabled (eq .Values.collector.implementation "agent") }}
              collector: http://{{ $fullname }}-collector:18193
{{- end }}
          request:
            path:
              agent: {type: string}
              path: {type: string}
{{- with $transportHeaders }}
            headers:
{{- include "chatbot-mesh.transportHeaderEntries" (dict "headers" . "indent" 14) }}
{{- end }}
        ui:
          method: GET
          path: /ui/{path...}
          binding: static_assets
          static_assets:
            root: "${CHATBOT_UI_ROOT:-agents/chatbot/ui/app/dist}"
            index: index.html
            spa: true
          request:
            path:
              path: {type: string}
{{- with $transportHeaders }}
            headers:
{{- include "chatbot-mesh.transportHeaderEntries" (dict "headers" . "indent" 14) }}
{{- end }}
        root_redirect:
          method: GET
          path: /
          binding: redirect
          redirect:
            location: /ui/
            status: 302
{{- with $transportHeaders }}
          request:
            headers:
{{- include "chatbot-mesh.transportHeaderEntries" (dict "headers" . "indent" 14) }}
{{- end }}
        # The read boundary the served interface calls (srd016 R5.2).
        documents:
          method: POST
          path: /api/v1/documents
          binding: machine_request
          request:
            body_schema:
              type: object
              required: [record_ids]
              properties:
                record_ids: {type: array}
            # Declared so the request is accepted carrying it; an undeclared header is refused outright.
            headers:
              x-analyst-clearance: {type: string}
{{- include "chatbot-mesh.transportHeaderEntries" (dict "headers" $transportHeaders "indent" 14) }}
          machine_request:
            profile: request-profile.yaml
            machine: document-machine.yaml
            timeout: 60s
            request:
              body:
                record_ids: $.record_ids
              headers:
                max_clearance: $.x-analyst-clearance
            response:
              terminal_states:
                ReadServed:
                  status: 200
                  content_type: application/json
                  body:
                    records: $.records
                    # The enforced level and nothing about what was withheld
                    # (srd016 R7.2, R7.3).
                    clearance_enforced_at: $.clearance_enforced_at
                Failed:
                  status: 500
                  content_type: application/json
                  body:
                    error: read_failed
                    detail: a boundary word failed before the read was dispatched
        documents_query:
          method: POST
          path: /api/v1/documents/query
          binding: machine_request
          request:
            body_schema:
              type: object
              required: [document_filter]
              properties:
                document_filter: {type: object, minProperties: 1}
            headers:
              x-analyst-clearance: {type: string}
{{- include "chatbot-mesh.transportHeaderEntries" (dict "headers" $transportHeaders "indent" 14) }}
          machine_request:
            profile: request-profile.yaml
            machine: document-filter-machine.yaml
            timeout: 60s
            request:
              body:
                document_filter: $.document_filter
              headers:
                max_clearance: $.x-analyst-clearance
            response:
              terminal_states:
                ReadServed:
                  status: 200
                  content_type: application/json
                  body:
                    records: $.records
                    clearance_enforced_at: $.clearance_enforced_at
                Failed:
                  status: 500
                  content_type: application/json
                  body:
                    error: read_failed
                    detail: a boundary word failed before the read was dispatched

    chatbot_control:
      address: 0.0.0.0:{{ .Values.chatbot.ports.control }}
      limits_ref: local_chatbot_control
      queue:
        name: chatbot_control
        capacity: 8
        timeout: 30s
      shutdown:
        timeout: 5s
        drain_policy: drain_then_stop
      # The exit route is agent-core's canonical one, injected at load
      # (GH-1264), as it is for the packaged profile.
      endpoints:
        health:
          method: GET
          path: /api/lifecycle/health
          binding: health
{{- end -}}

{{/*
The chatbot monitor-rest.yaml: an instantiation of agent-core's monitor server
fragment with the in-cluster bind address, and the limits that bound it. The
fragment is the one definition of the monitor surface, so the chart supplies
only what varies in the cluster, the address and its port. It is its own file
because the chatbot's rest.yaml is shared with its request profiles, which
launch no monitor server (srd052 R3.1).
*/}}
{{- define "chatbot-mesh.chatbotMonitorRest" -}}
kind: rest-definition
schema_version: 1
unit: cohere-chatbot-monitor-rest
expand:
  - fragment: /opt/agent-core/tools/rest/units/monitor-server-fragment.yaml
    args: {address: "0.0.0.0:{{ .Values.chatbot.ports.monitor }}", limits_ref: local_chatbot_monitor, queue_name: chatbot_monitor}
rest:
  version: v1
  limits:
    local_chatbot_monitor:
      timeout: 5s
      read_timeout: 5s
      max_request_bytes: 4096
      max_response_bytes: 1048576
      redirect:
        mode: none
      network:
        schemes: [http]
        hosts: [127.0.0.1, localhost]
        ports: [{{ .Values.chatbot.ports.monitor }}]
        allow_public_listener: true
{{- end -}}

{{/* transportHeaderEntries renders one string header declaration per name in
     .headers, indented by .indent, each on its own line (#883). */}}
{{- define "chatbot-mesh.transportHeaderEntries" -}}
{{- range .headers }}
{{ printf "%s: {type: string}" . | indent $.indent }}
{{- end }}
{{- end -}}
