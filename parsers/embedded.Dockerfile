# check=skip=InvalidDefaultArgInFrom
# The container that builds the embedded fallback parser module byte for byte
# the same on every machine. RUST_VERSION comes from rust-toolchain.toml; the
# digest pins the image, and embedded.lock repeats both so a build can check
# that the three agree.
ARG RUST_VERSION
FROM rust:${RUST_VERSION}-slim@sha256:17d1ba895198f9934c6314ec5346a0d5115372f3243390c3d731e242f35c2f27

# The build runs as the caller's uid, which cannot write to the image's rustup
# home: whatever rust-toolchain.toml lists is installed here. A cargo-home
# volume created from /cargo inherits its mode.
WORKDIR /toolchain
COPY rust-toolchain.toml ./
RUN rustup toolchain install --no-self-update && rustc -vV && mkdir -m 0777 /cargo
