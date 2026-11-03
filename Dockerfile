# NEUROFORGE X control plane — multi-stage Go build.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/neuroforge-server ./cmd/neuroforge-server \
 && CGO_ENABLED=0 go build -o /out/neuroforge ./cmd/neuroforge

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/neuroforge-server /neuroforge-server
COPY --from=build /out/neuroforge /neuroforge
COPY --from=build /src/examples /examples
EXPOSE 8080
ENV NEUROFORGE_ADDR=:8080
ENTRYPOINT ["/neuroforge-server"]
