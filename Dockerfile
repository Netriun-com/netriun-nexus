FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /nexus ./cmd/nexus

FROM gcr.io/distroless/static-debian12@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
ARG VERSION=dev
ARG REVISION=unknown
ARG SOURCE=https://github.com/Netriun-com/netriun-nexus
COPY --from=build /nexus /usr/local/bin/nexus
USER 65532:65532
EXPOSE 8080
LABEL org.opencontainers.image.title="Netriun Nexus" \
      org.opencontainers.image.description="Multi-cloud orchestration control plane" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$REVISION \
      org.opencontainers.image.source=$SOURCE \
      org.opencontainers.image.licenses="UNLICENSED"
ENTRYPOINT ["nexus"]
