# syntax=docker/dockerfile:1

# Stage 1: Extract binary from dist/ (built by pack.ts or GoReleaser)
FROM scratch AS binary
ARG TARGETARCH
COPY dist/datamitsu-linux-${TARGETARCH}/datamitsu /datamitsu

# Stage 2: Runtime
FROM debian:trixie-slim

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
    && rm -rf /var/lib/apt/lists/*

# Copy binary
COPY --from=binary /datamitsu /usr/local/bin/datamitsu

# Create non-root user and setup workspace
ARG USER_UID=1000
WORKDIR /workspace
RUN chmod +x /usr/local/bin/datamitsu && \
    useradd -m -l -u ${USER_UID} -s /bin/bash datamitsu && \
    chown datamitsu:datamitsu /workspace

USER datamitsu

ENTRYPOINT ["/usr/local/bin/datamitsu"]
CMD ["--help"]
