FROM golang:1.24-alpine

# Install FFmpeg, a required system dependency
RUN apk add --no-cache ffmpeg

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o /anime-api .

EXPOSE 8081

CMD [ "/anime-api" ]