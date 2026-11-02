# Public runtime image (GH-934, the GH-928 decision): the binary is published,
# the source is not. This recipe derives the public image from the pinned
# private agent-core release and adds the /licenses layer a binary
# redistribution owes -- declarative-agents is dual-licensed (Nokia-origin
# files BSD-3-Clause, post-fork files MIT), and BSD-3-Clause requires the
# copyright notice and disclaimer to accompany redistributions in binary form.
# The copies under images/licenses/ are tracked at the pinned commit;
# runtime:mirror refuses a build whose copies have drifted from the pin when
# the core checkout is present to compare against.
#
# Build context is the images directory; RUNTIME_IMAGE selects the private
# pinned base, and the pushed artifact carries its layers, so pulling the
# public image never touches the private repository:
#   docker build -f images/agent-core-public.Dockerfile \
#     --build-arg RUNTIME_IMAGE=<agent-core>:<pin> -t <public>/agent-core:<pin> images
ARG RUNTIME_IMAGE=ghcr.io/mesh-intelligence/declarative-agents/agent-core:v0.20261005.0

FROM ${RUNTIME_IMAGE}
LABEL org.opencontainers.image.source="https://github.com/mesh-intelligence/cohere-demo" \
      org.opencontainers.image.licenses="BSD-3-Clause AND MIT" \
      org.opencontainers.image.description="declarative-agents runtime, binary redistribution for the cohere-demo application; licences under /licenses"
COPY licenses/ /licenses/
