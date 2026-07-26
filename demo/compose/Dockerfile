# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.24
ARG DR_VERSION=11.91.20545
ARG DR_SHA256=db8b9f3d0dc14a469da1dc791e120a6cc6d968d2b18facb7bcaa471f4bde22da
ARG LEAN_EVALUATOR_REF=cb99cfc95748772d4ea67d63f3875c2d574660fc

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS shimmy-build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=demo
ARG COMMIT=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false \
      -ldflags "-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT}" \
      -o /out/shimmy .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd examples/demo-stateful && \
    GOOS=wasip1 GOARCH=wasm go build -trimpath -buildvcs=false \
      -buildmode=c-shared -o /out/generic-evaluator.wasm .

FROM busybox:1.37.0-musl AS generic
COPY --from=shimmy-build /out/shimmy /shimmy
COPY --from=shimmy-build /out/generic-evaluator.wasm /opt/evaluators/generic.wasm
ENV LOG_LEVEL=error \
    FUNCTION_INTERFACE=wasm \
    FUNCTION_WASM_PROFILE=generic \
    FUNCTION_WASM_MODULE=/opt/evaluators/generic.wasm \
    FUNCTION_MAX_PROCS=1 \
    FUNCTION_WORKER_SEND_TIMEOUT=15s
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/shimmy"]
CMD ["serve", "--host", "0.0.0.0", "--port", "8080"]

FROM busybox:1.37.0-musl AS python-reactor
COPY --from=shimmy-build /out/shimmy /shimmy
COPY build/python-reactor/artifacts/ /opt/runtimes/agent-python/
COPY examples/eval-python/eval.py /opt/evaluators/eval.py
RUN echo "90c27951b2d8c2c7a8b42705b365cb4231c6dad207aad5260d55d2f9a85f1034  /opt/runtimes/agent-python/agent-python-runtime-numpy-core.wasm" | sha256sum -c -
ENV LOG_LEVEL=error \
    FUNCTION_INTERFACE=wasm \
    FUNCTION_WASM_PROFILE=agent-python \
    FUNCTION_WASM_MODULE=/opt/runtimes/agent-python/agent-python-runtime-numpy-core.wasm \
    FUNCTION_WASM_MANIFEST=/opt/runtimes/agent-python/manifest.json \
    FUNCTION_WASM_PYTHON_SCRIPT=/opt/evaluators/eval.py \
    FUNCTION_WASM_MAX_MEMORY_PAGES=8192 \
    FUNCTION_MAX_PROCS=1 \
    FUNCTION_WORKER_SEND_TIMEOUT=60s
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/shimmy"]
CMD ["serve", "--host", "0.0.0.0", "--port", "8080"]

FROM --platform=$TARGETPLATFORM node:22-bookworm-slim AS pyodide-scipy
WORKDIR /opt/pyodide
COPY examples/eval-pyodide/package.json examples/eval-pyodide/package-lock.json ./
RUN npm ci --omit=dev && npm cache clean --force
RUN node -e "(async()=>{const {loadPyodide}=require('pyodide');const p=await loadPyodide();await p.loadPackage(['scipy']);})().catch((e)=>{console.error(e);process.exit(1)})"
COPY --from=shimmy-build /out/shimmy /shimmy
COPY examples/eval-pyodide/runner.js examples/eval-pyodide/framed-stdio.js /opt/pyodide/
COPY examples/eval-scipy/eval.py /opt/evaluators/eval.py
RUN node --check /opt/pyodide/runner.js && \
    node --check /opt/pyodide/framed-stdio.js && \
    node -e "const f=require('/opt/pyodide/framed-stdio');if(typeof f.FramedReader!=='function'||typeof f.encodeFrame!=='function')process.exit(1)" && \
    chmod 0755 /opt/evaluators && \
    chmod 0644 /opt/pyodide/runner.js /opt/pyodide/framed-stdio.js /opt/evaluators/eval.py
ENV LOG_LEVEL=error \
    FUNCTION_INTERFACE=pyodide \
    FUNCTION_PYODIDE_RUNNER=/opt/pyodide/runner.js \
    FUNCTION_PYODIDE_SCRIPT=/opt/evaluators/eval.py \
    FUNCTION_PYODIDE_PACKAGES=scipy \
    FUNCTION_MAX_PROCS=1 \
    FUNCTION_WORKER_SEND_TIMEOUT=120s
USER node
EXPOSE 8080
ENTRYPOINT ["/shimmy"]
CMD ["serve", "--host", "0.0.0.0", "--port", "8080"]

FROM --platform=linux/amd64 ghcr.io/lambda-feedback/evaluation-function-base/lean:4.8.0-rc2 AS lean-build
ARG LEAN_EVALUATOR_REF
COPY demo/compose/dbi-lean/Evaluation.lean /tmp/Evaluation.lean
RUN apt-get update >/dev/null && \
    apt-get install -y --no-install-recommends git ca-certificates >/dev/null && \
    rm -rf /var/lib/apt/lists/* /app/* && \
    git clone https://github.com/lambda-feedback/compareLeanTest.git /app && \
    cd /app && \
    git checkout "${LEAN_EVALUATOR_REF}" && \
    test "$(git rev-parse HEAD)" = "${LEAN_EVALUATOR_REF}" && \
    cp /tmp/Evaluation.lean src/Evaluation.lean && \
    lake build evaluation && \
    cp .lake/build/bin/evaluation /tmp/lean-evaluation

FROM --platform=linux/amd64 ubuntu:22.04 AS dr-build
ARG DR_VERSION
ARG DR_SHA256
RUN apt-get update >/dev/null && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      build-essential ca-certificates curl >/dev/null && \
    rm -rf /var/lib/apt/lists/*
RUN curl -fL --retry 3 \
      "https://github.com/DynamoRIO/dynamorio/releases/download/cronbuild-${DR_VERSION}/DynamoRIO-Linux-${DR_VERSION}.tar.gz" \
      -o /tmp/dr.tar.gz && \
    echo "${DR_SHA256}  /tmp/dr.tar.gz" | sha256sum -c - && \
    mkdir -p /opt/dr && \
    tar -xzf /tmp/dr.tar.gz --strip-components=1 -C /opt/dr && \
    rm /tmp/dr.tar.gz
COPY demo/compose/dbi-lean/path_policy.c /src/path_policy.c
RUN mkdir -p /out && \
    gcc -shared -fPIC -O2 -Wall -Wextra -DLINUX -DX86_64 -include stdint.h \
      -I/opt/dr/include -I/opt/dr/ext/include \
      -Wl,-rpath,/opt/dr/lib64/release \
      -o /out/path_policy.so /src/path_policy.c \
      -L/opt/dr/lib64/release -ldynamorio
RUN mkdir -p /out/dr/bin64 /out/dr/lib64/release /out/dr/lib64/debug \
      /out/dr/lib32/release /out/dr/lib32/debug && \
    cp /opt/dr/bin64/drrun /out/dr/bin64/ && \
    for helper in drinject drconfig drnudgeunix; do \
      test ! -f "/opt/dr/bin64/${helper}" || cp "/opt/dr/bin64/${helper}" /out/dr/bin64/; \
    done && \
    cp /opt/dr/lib64/release/libdynamorio.so /opt/dr/lib64/release/libdrpreload.so \
      /out/dr/lib64/release/ && \
    cp /opt/dr/lib64/release/libdynamorio.so /out/dr/lib64/debug/ && \
    cp /opt/dr/lib64/release/libdynamorio.so /out/dr/lib32/release/ && \
    cp /opt/dr/lib64/release/libdynamorio.so /out/dr/lib32/debug/

FROM --platform=linux/amd64 ubuntu:22.04 AS dbi-lean
RUN apt-get update >/dev/null && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      ca-certificates curl libunwind8 >/dev/null && \
    rm -rf /var/lib/apt/lists/*
COPY --from=shimmy-build /out/shimmy /shimmy
COPY --from=lean-build /tmp/lean-evaluation /opt/evaluators/lean-evaluation
COPY --from=dr-build /out/dr /opt/dr
COPY --from=dr-build /out/path_policy.so /opt/dbi/path_policy.so
COPY demo/compose/dbi-lean/entrypoint.sh /entrypoint.sh
RUN chmod 0755 /shimmy /opt/evaluators/lean-evaluation /opt/dr/bin64/drrun /entrypoint.sh && \
    chmod 0644 /opt/dbi/path_policy.so
ENV LOG_LEVEL=error \
    HOME=/tmp \
    TMPDIR=/tmp \
    DYNAMORIO_HOME=/opt/dr \
    DYNAMORIO_CONFIGDIR=/tmp/dynamorio-config \
    FUNCTION_INTERFACE=file \
    FUNCTION_COMMAND=/opt/evaluators/lean-evaluation \
    FUNCTION_DBI_SECURITY_ENABLED=true \
    FUNCTION_DBI_DRRUN=/opt/dr/bin64/drrun \
    FUNCTION_DBI_CLIENT=/opt/dbi/path_policy.so \
    FUNCTION_DBI_OPTIONS="-logdir /tmp/dynamorio-logs" \
    FUNCTION_MAX_PROCS=1 \
    FUNCTION_WORKER_SEND_TIMEOUT=60s
EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]
CMD ["serve", "--host", "0.0.0.0", "--port", "8080"]
