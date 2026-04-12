# syntax=docker/dockerfile:1

# Stage 1: Extract binary from dist/ (built by pack.ts or GoReleaser)
FROM scratch AS binary
ARG TARGETARCH
COPY dist/datamitsu-linux-${TARGETARCH}/datamitsu /datamitsu

# Stage 2: Runtime
FROM debian:bookworm-slim

# Install runtime dependencies
# hadolint ignore=DL3008
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    git \
    && rm -rf /var/lib/apt/lists/*

# Copy binary
COPY --from=binary /datamitsu /usr/local/bin/datamitsu

# Create non-root user and setup workspace
WORKDIR /workspace
RUN chmod +x /usr/local/bin/datamitsu && \
    useradd -m -u 1000 -s /bin/bash datamitsu && \
    chown datamitsu:datamitsu /workspace

USER datamitsu

ENTRYPOINT ["/usr/local/bin/datamitsu"]
CMD ["--help"]
