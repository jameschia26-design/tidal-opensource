# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG COMMIT_SHA=development
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.buildCommit=${COMMIT_SHA}" -o /out/tidal .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/tidal /tidal
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/tidal"]
