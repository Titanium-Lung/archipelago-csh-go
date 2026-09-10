FROM golang:alpine

WORKDIR /app

COPY . .

RUN go mod download

RUN go build -o /archipelago

EXPOSE 5001

CMD ["/archipelago"]