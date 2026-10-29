# Build em duas etapas: a primeira tem toda a toolchain do Go, a segunda só
# carrega o binário já compilado — a imagem final não leva compilador nem
# código-fonte, só o executável. Mesmo padrão de services/templatesadmin/Dockerfile.
# Compila na arquitetura de quem constrói para a do alvo (sem emulação,
# rápido num build multi-arquitetura).
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
ARG TARGETOS TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/launcher ./cmd/launcher

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/launcher /usr/local/bin/launcher

ENTRYPOINT ["/usr/local/bin/launcher"]
