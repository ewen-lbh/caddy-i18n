# caddy-i18n

Use standard [GNU Gettext PO translation files](https://www.gnu.org/software/gettext/manual/html_node/PO-Files.html) to translate HTML responses served via a [Caddy](https://caddyserver.com/) web server.

## Installation

Install [xcaddy](https://caddyserver.com/docs/build#xcaddy)

```bash
xcaddy build --with github.com/ewen-lbh/caddy-i18n@v0.1.2
```

## Usage

See the [example Caddyfile](./example/Caddyfile) for a simple example.

You can see what the final responses look like in [`./example/responses`](./example/responses). The example .po files used are in [`./example/messages`](./example/messages).
