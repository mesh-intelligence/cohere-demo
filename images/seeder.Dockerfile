# Seeder image (GH-937, the GH-928 decision): the compiled magefiles binary
# with the publishable case baked in, so a cluster grounds the demo without a
# Go toolchain, private modules, or org access. seeder:publish compiles the
# binary per platform on the host -- the build context carries binaries, the
# case store, and the licence notices, never Go source -- and this recipe only
# assembles them. The baked case is cases/investment-research, whose every
# tier is publishable; the keep-private store families never enter the
# context (srd017 R9.7).
#
#   mage seeder:publish
ARG BASE_IMAGE=docker.io/library/alpine:3.22

FROM ${BASE_IMAGE}
ARG TARGETARCH
LABEL org.opencontainers.image.source="https://github.com/mesh-intelligence/cohere-demo" \
      org.opencontainers.image.licenses="BSD-3-Clause AND MIT" \
      org.opencontainers.image.description="cohere-demo corpus seeder: seeds the mesh's Chroma collections from the baked publishable store; licences under /licenses"
COPY licenses/ /licenses/
# The case lands under a directory literally named cases/<case name>: the case
# definition -- its embed selection above all -- resolves from that path shape,
# exactly as it does in the repository tree.
COPY case/ /srv/cases/investment-research/
COPY seeder-linux-${TARGETARCH} /usr/local/bin/seeder
ENV SEED_CASE_ROOT=/srv/cases/investment-research
USER 65534:65534
ENTRYPOINT ["/usr/local/bin/seeder", "corpus:seedCluster"]
