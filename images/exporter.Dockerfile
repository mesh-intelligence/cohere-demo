# Exporter image (srd013 R6.2, R6.3): agent-core plus the rendering toolchain
# the exporter's exec words run -- pandoc for md_to_tex and XeTeX for
# render_pdf -- a recorded, sanctioned divergence from the binary-only image
# contract, the applier image's exemption. The binaries and the TeX tree are
# image packaging; the profile declares the words and their confinement flags
# (shell escape off, paranoid file access), and this image supplies the tools.
#
# The package set is what an offline render needs and was proven end to end
# in a scratch container before it was pinned: pandoc-cli converts GitHub
# markdown to standalone LaTeX, texlive-xetex is the engine, latexextra
# carries unicode-math and the styles pandoc's template loads, and
# fontsrecommended carries the Latin Modern OpenType faces fontspec defaults
# to. fc-cache runs at build so the first render pays no font-scan latency.
# The tree adds roughly 900 MB over agent-core; the egress-denied workload
# cannot fetch a missing package at run time, so completeness here is the
# render's availability.
#
# Build context is the application root; RUNTIME_IMAGE selects the agent-core
# base the mesh already runs:
#   docker build -f images/exporter.Dockerfile \
#     --build-arg RUNTIME_IMAGE=<agent-core> -t <repo>/exporter:<rev> .
ARG RUNTIME_IMAGE=ghcr.io/mesh-intelligence/declarative-agents/agent-core:v0.20261005.0

FROM ${RUNTIME_IMAGE}
# The runtime image's ENV strips /sbin from PATH, so apk goes by full path.
# The published agent-core release already runs as an unprivileged agent user,
# so the install switches to root and the user is created only on a base that
# lacks it (a locally built runtime image). The exporter then runs as agent:
# it renders untrusted documents, and a non-root process is one more wall
# behind the engine's confinement flags and the workload's NetworkPolicy. The
# work directory is an emptyDir (world-writable by default) and the projected
# profiles are world-readable, so no mount needs an ownership arrangement.
USER root
RUN /sbin/apk add --no-cache \
    fontconfig \
    pandoc-cli \
    texlive-xetex \
    texmf-dist-fontsrecommended \
    texmf-dist-latexextra \
    && fc-cache -f \
    && (id agent >/dev/null 2>&1 || /usr/sbin/adduser -D -u 10001 agent)
USER agent:agent
