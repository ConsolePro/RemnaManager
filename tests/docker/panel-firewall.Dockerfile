FROM debian:bookworm
RUN apt-get update && apt-get install -y --no-install-recommends nftables iproute2 python3 ca-certificates && rm -rf /var/lib/apt/lists/*
