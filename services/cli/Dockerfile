# O basestack e o bs numa imagem -- é assim que chegam às outras máquinas:
#   - /usr/local/bin: os desta arquitetura ("basestack upgrade" tira-os);
#   - /dist: os de todos os sistemas (<cmd>-<os>-<arch>), que a plataforma
#     serve a quem ainda não tem nenhum (curl .../v1/cli/install.sh | sh).
# Compila na arquitetura de quem constrói, sem emulação, e uma vez só.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS dist
ARG VERSION=dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN for os in linux darwin; do for arch in amd64 arm64; do for cmd in basestack bs; do \
      CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -ldflags "-X main.version=$VERSION" -o /dist/$cmd-$os-$arch ./cmd/$cmd || exit 1; \
    done; done; done

FROM --platform=$BUILDPLATFORM alpine:3.20 AS pick
ARG TARGETOS TARGETARCH
COPY --from=dist /dist /dist
RUN mkdir /out && cp /dist/basestack-$TARGETOS-$TARGETARCH /out/basestack && cp /dist/bs-$TARGETOS-$TARGETARCH /out/bs

# Não corre: só transporta os binários.
FROM scratch
COPY --from=pick /out/basestack /out/bs /usr/local/bin/
COPY --from=dist /dist /dist
CMD ["/usr/local/bin/basestack"]
