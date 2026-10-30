# O basestack e o bs numa imagem -- é assim que chegam às outras máquinas:
# "basestack upgrade" busca-a ao registry e tira de lá o binário da sua
# arquitetura. Compila na arquitetura de quem constrói, sem emulação.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG TARGETOS TARGETARCH
ARG VERSION=dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "-X main.version=$VERSION" -o /out/basestack ./cmd/basestack && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "-X main.version=$VERSION" -o /out/bs ./cmd/bs

# Não corre: só transporta os binários.
FROM scratch
COPY --from=builder /out/basestack /out/bs /usr/local/bin/
CMD ["/usr/local/bin/basestack"]
