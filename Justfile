build:
	go mod tidy
	xcaddy build --with github.com/ewen-lbh/caddy-i18n=. --output ~/.local/bin/caddy-i18n

dev:
	just build
	caddy-i18n adapt --config example/Caddyfile | jq .
	caddy-i18n run --config example/Caddyfile

example:
	just build
	caddy-i18n start --config example/Caddyfile
	wget -O example/responses/fr.html http://localhost:8081/fr
	wget -O example/responses/en.html http://localhost:8081/en
	caddy-i18n stop --config example/Caddyfile

