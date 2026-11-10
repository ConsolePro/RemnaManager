FROM debian:12-slim
RUN apt-get update && apt-get install -y --no-install-recommends openssh-server ca-certificates && rm -rf /var/lib/apt/lists/* && mkdir -p /run/sshd
