FROM golang:1.27.1-bookworm AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/archipelago

FROM python:3.12-slim-bookworm
COPY --from=ghcr.io/astral-sh/uv:0.12.22 /uv /usr/local/bin/uv

WORKDIR /app

COPY requirements.txt .
RUN uv pip install --system --no-cache -r requirements.txt

COPY --from=builder /out/archipelago ./archipelago
COPY ./pyscripts ./pyscripts
COPY ./Archipelago-0.6.8 ./Archipelago-0.6.8

EXPOSE 5001

ENV PYTHONUNBUFFERED=1

CMD ["./archipelago"]