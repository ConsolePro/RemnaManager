FROM debian:12-slim
RUN apt-get update && apt-get install -y --no-install-recommends nginx openssl openssh-server nftables jq iproute2 python3 curl ca-certificates shellcheck && rm -rf /var/lib/apt/lists/* && mkdir -p /run/sshd /run/systemd/system /opt/fixture-bin && mv /etc/nginx /opt/fixture-nginx
COPY fixtures/node-agent-command.py /opt/fixture-command.py
RUN chmod 0755 /opt/fixture-command.py && for command in docker systemctl systemd-run dpkg-query certbot getent curl sh; do ln -s /opt/fixture-command.py /opt/fixture-bin/$command; done
RUN ln -s /opt/fixture-command.py /bin/systemctl
ENV PATH="/opt/fixture-bin:${PATH}"
