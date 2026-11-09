#!/usr/bin/env bash
site_html() {
    local title subtitle color
    case ${1:-studio} in
        studio) title='North Studio'; subtitle='Thoughtful design. Lasting impressions.'; color='#e7e0d5';;
        journal) title='Field Notes'; subtitle='A journal of places, ideas and everyday discoveries.'; color='#dbe7df';;
        workshop) title='The Workshop'; subtitle='Carefully made, one detail at a time.'; color='#e7dfe8';;
        *) die 'Неизвестный шаблон.';;
    esac
    cat <<EOF
<!doctype html><html lang="en"><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>$title</title>
<style>body{margin:0;background:$color;color:#252828;font:18px system-ui}main{max-width:800px;margin:15vh auto;padding:32px}h1{font-size:clamp(40px,8vw,80px);letter-spacing:-.05em}article{border-top:1px solid #888;padding:24px 0;margin-top:50px}p{line-height:1.7}</style>
<main><small>EST. 2020 / INDEPENDENT PROJECT</small><h1>$title</h1><p>$subtitle</p>
<article><h2>About our work</h2><p>We bring together people who value simplicity, curiosity and craft.
Our next collection is taking shape. Come back soon for new stories and projects.</p></article>
<footer>© 2026 $title</footer></main></html>
EOF
}
http_site() {
    cat <<EOF
server {
    listen 80; listen [::]:80;
    server_name $1;
    server_tokens off;
    location ^~ /.well-known/acme-challenge/ { root /var/www/remnamanager-acme; }
    location / { root /var/www/remnamanager/$1; try_files \$uri \$uri/ =404; }
}
EOF
}
proxy_settings() {
    cat <<EOF
        proxy_pass http://127.0.0.1:$1;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$remote_addr;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-Host \$host;
        proxy_set_header X-Forwarded-Port 443;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$rm_connection;
        proxy_read_timeout 300s;
EOF
}
tls_site() {
    local name=$1 listen_port=${2:-443} upstream=${3:-} gate=${4:-}
    if [[ -n $gate ]]; then
        [[ $gate =~ ^[a-f0-9]{64}$ ]] || die 'Неверный секрет входа.'
        printf 'map $cookie_rm_access_%s $rm_allowed { default 0; "%s" 1; }\n' "${gate:0:12}" "$gate"
    fi
    printf 'server {\n'
    if [[ $listen_port == 443 ]]; then printf '    listen 443 ssl http2; listen [::]:443 ssl http2;\n'
    else printf '    listen 127.0.0.1:%s ssl http2;\n' "$listen_port"; fi
    cat <<EOF
    server_name $name;
    server_tokens off;
    ssl_certificate /etc/letsencrypt/live/$name/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/$name/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_session_tickets off;
EOF
    if [[ -n $gate ]]; then
        cat <<EOF
    access_log off;
    add_header Referrer-Policy no-referrer always;
    location = /enter-$gate {
        add_header Set-Cookie "rm_access_${gate:0:12}=$gate; Path=/; Secure; HttpOnly; SameSite=Strict; Max-Age=86400" always;
        add_header Referrer-Policy no-referrer always;
        return 302 /auth/login;
    }
    location / {
        error_page 418 = @panel;
        if (\$rm_allowed = 1) { return 418; }
        root /var/www/remnamanager/$name;
        try_files \$uri \$uri/ =404;
    }
    location @panel {
EOF
        proxy_settings "$upstream"
        printf '    }\n'
    elif [[ -n $upstream ]]; then
        printf '    location / {\n'; proxy_settings "$upstream"; printf '    }\n'
    else printf '    location / { root /var/www/remnamanager/%s; try_files $uri $uri/ =404; }\n' "$name"; fi
    printf '}\n'
}
reality_config() {
    jq -n --arg domain "$1" --arg key "$2" --arg short "$3" --arg tag "${4:-VLESS_REALITY}" '{log:{loglevel:"warning"},inbounds:[{tag:$tag,listen:"0.0.0.0",port:443,protocol:"vless",settings:{clients:[],decryption:"none"},sniffing:{enabled:true,destOverride:["http","tls","quic"],routeOnly:true},streamSettings:{network:"raw",security:"reality",realitySettings:{show:false,target:"127.0.0.1:8443",xver:0,serverNames:[$domain],privateKey:$key,shortIds:[$short]}}}],outbounds:[{tag:"DIRECT",protocol:"freedom"},{tag:"BLOCK",protocol:"blackhole"}],routing:{rules:[{type:"field",ip:["geoip:private"],outboundTag:"BLOCK"}]}}'
}
node_compose() {
    jq -n --arg secret "$1" '{services:{remnanode:{image:"remnawave/node:latest",container_name:"remnanode",restart:"unless-stopped",network_mode:"host",environment:{NODE_PORT:"2222",SECRET_KEY:$secret},logging:{driver:"json-file",options:{"max-size":"10m","max-file":"3"}}}}}'
}
subscription_compose() {
    jq -n --arg image "${1:-remnawave/subscription-page:latest}" '{services:{subscription:{image:$image,restart:"unless-stopped",env_file:[".env"],ports:["127.0.0.1:3010:3010"],networks:["remnawave-network"],logging:{driver:"json-file",options:{"max-size":"10m","max-file":"3"}}}},networks:{"remnawave-network":{external:true}}}'
}
checker_compose() {
    jq -n '{services:{checker:{image:"kutovoys/xray-checker:latest",restart:"unless-stopped",env_file:[".env"],ports:["127.0.0.1:2112:2112"],logging:{driver:"json-file",options:{"max-size":"10m","max-file":"3"}}}}}'
}
warp_config() {
    local enabled=$1 domains=${2:-'[]'}
    jq --argjson enabled "$enabled" --argjson domains "$domains" '
      .outbounds = [(.outbounds // [])[] | select(.tag != "RM_WARP")] |
      .routing.rules = [(.routing.rules // [])[] | select(.outboundTag != "RM_WARP")] |
      if $enabled then .outbounds += [{tag:"RM_WARP",protocol:"freedom",settings:{},streamSettings:{sockopt:{interface:"warp",mark:51888,domainStrategy:"ForceIPv4",tcpFastOpen:true}}}] |
        .routing.rules = [{type:"field",domain:($domains|map("domain:"+.)),outboundTag:"RM_WARP"}] + .routing.rules else . end'
}
