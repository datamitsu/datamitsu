# check=skip=InvalidDefaultArgInFrom
# The container that builds the embedded fallback parser module byte for byte
# the same on every machine. RUST_VERSION comes from rust-toolchain.toml; the
# digest pins the image, and embedded.lock repeats both so a build can check
# that the three agree.
ARG RUST_VERSION
FROM rust:${RUST_VERSION}-slim@sha256:1a939748a7cbab172de91a014e3804a97af1394a8c844faa67fce9401c4e40fb
ARG RUST_VERSION

# The digest pins the image, not the release its tag names: an image of
# another rustc would install the requested release below and pass every later
# check, so its own compiler is checked first.
RUN release="$(rustc -vV | sed -n 's/^release: //p')" \
  && if [ "$release" != "$RUST_VERSION" ]; then \
    echo "the pinned image runs rustc $release, rust-toolchain.toml names $RUST_VERSION" >&2; exit 1; \
  fi

# The build runs as the caller's uid, which cannot write to the image's rustup
# home: whatever rust-toolchain.toml lists is installed here. A cargo-home
# volume created from /cargo inherits its mode.
WORKDIR /toolchain
COPY rust-toolchain.toml ./
RUN rustup toolchain install --no-self-update && rustc -vV && mkdir -m 0777 /cargo
