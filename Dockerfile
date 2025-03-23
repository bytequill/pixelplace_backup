FROM golang:1.24-alpine

WORKDIR /usr/src/app

# CGO requirements
RUN apk add --no-cache pkgconf gcc musl-dev

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ENV CGO_ENABLED=1
RUN go build -v -o /usr/local/bin/app ./...

ENV IS_CONTAINER=1
RUN mkdir -p /usr/src/app/data
CMD ["app"]