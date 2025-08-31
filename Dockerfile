# Start with a modern, supported Debian base image
FROM debian:bookworm-slim

# Prevent interactive prompts during installation
ENV DEBIAN_FRONTEND=noninteractive

# Install dependencies: sudo, curl, gnupg, and Xvfb (for the virtual display)
RUN apt-get update && apt-get install -y \
    sudo \
    curl \
    gnupg \
    xvfb \
    --no-install-recommends && \
    rm -rf /var/lib/apt/lists/*

# Add the Brave Browser repository and install Brave
RUN sudo curl -fsSLo /usr/share/keyrings/brave-browser-archive-keyring.gpg https://brave-browser-apt-release.s3.brave.com/brave-browser-archive-keyring.gpg && \
    echo "deb [signed-by=/usr/share/keyrings/brave-browser-archive-keyring.gpg] https://brave-browser-apt-release.s3.brave.com/ stable main" | sudo tee /etc/apt/sources.list.d/brave-browser-release.list && \
    sudo apt-get update && \
    sudo apt-get install -y brave-browser

# Set the Linux path for Brave so our Go app can find it
ENV BRAVE_PATH=/usr/bin/brave-browser

# Install Go
RUN curl -sL https://go.dev/dl/go1.22.0.linux-amd64.tar.gz | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:${PATH}"

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o /anime-api .

EXPOSE 8080

# xvfb-run creates a virtual screen for Brave to run inside the server.
CMD ["xvfb-run", "--auto-servernum", "/anime-api", "--no-sandbox"]