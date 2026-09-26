FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /godesk-sms ./cmd/godesk-sms

FROM alpine:3.22

RUN apk add --no-cache ca-certificates wget \
    && adduser -D -H -u 65532 godesk
COPY --from=build /godesk-sms /godesk-sms
USER godesk
EXPOSE 8080
ENTRYPOINT ["/godesk-sms"]
