# syntax=docker/dockerfile:1

# Stage 1: Extract binary from dist/ (built by pack.ts or GoReleaser)
FROM scratch AS binary
ARG TARGETARCH
COPY dist/datamitsu-linux-${TARGETARCH}/datamitsu /datamitsu

# Stage 2: Runtime
FROM debian:trixie-20260803-slim@sha256:3a39a0592364683e6bab97937b72cad5a8fa6dcbbee90edb3bb48c7f8e94f258

# Static OCI labels (dynamic labels injected via docker/metadata-action in CI)
LABEL org.opencontainers.image.title="datamitsu" \
      org.opencontainers.image.description="Configuration management and binary distribution tool. JavaScript-configurable tool orchestration written in Go." \
      org.opencontainers.image.vendor="datamitsu" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.source="https://github.com/datamitsu/datamitsu" \
      org.opencontainers.image.url="https://github.com/datamitsu/datamitsu" \
      org.opencontainers.image.documentation="https://datamitsu.com"

# Install runtime dependencies
# hadolint ignore=DL3008
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    git \
    libatomic1 \
    && rm -rf /var/lib/apt/lists/*

# Copy binary
COPY --from=binary /datamitsu /usr/local/bin/datamitsu

# Create non-root user, workspace and store root.
# Group 0 with group perms mirroring the owner's: k8s and OpenShift run
# containers under a random non-root uid whose only predictable membership is
# group 0, and datamitsu must be able to write the cache and hydrate the store
# at run time (direct installs or OCI seeding) under that uid too.
ARG USER_UID=1000
WORKDIR /workspace
RUN chmod +x /usr/local/bin/datamitsu && \
    useradd -m -l -u ${USER_UID} -s /bin/bash datamitsu && \
    mkdir -p /dm && \
    chown -R datamitsu:0 /dm /workspace && \
    chmod -R g=u /dm /workspace

# Fixed store/cache root so seeding and caching work identically for the named
# user and arbitrary uids (no dependence on $HOME, which is "/" for uids
# without a passwd entry).
ENV DATAMITSU_CACHE_DIR=/dm

USER datamitsu

ENTRYPOINT ["/usr/local/bin/datamitsu"]
CMD ["--help"]
