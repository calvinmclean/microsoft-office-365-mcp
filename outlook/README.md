# Microsoft 365 Outlook MCP Server

An MCP server for Microsoft Outlook integration, providing comprehensive email management capabilities through Microsoft Graph API.

## Features

- Read, send, draft, delete, and organize emails and messages
- List and navigate through all mail folders
- Powerful query-based email filtering by subject, sender, date, and folder
- List, download, and read email attachments with markdown conversion
- Create, edit, and send draft emails

## Usage

This MCP server is designed to run as part of [Obot](https://github.com/obot-platform/obot/). You can run Obot yourself or try it out on our free demo environment at [chat.obot.ai](https://chat.obot.ai).

## Local development with Docker Compose and OAuth

The Compose setup runs Outlook, PostgreSQL, and OAuth proxy `v0.0.3`. You need Docker and a Microsoft Entra app registration with a Web redirect URI of `http://localhost:8080/callback`. Configure the Microsoft Graph delegated permissions listed in `SCOPES_SUPPORTED` in `docker-compose.yml` and grant consent as required by your tenant. The registration's supported account types must match the accounts you use with the configured `common` authority.

Set these variables in the same terminal where you run Compose:

```bash
export OAUTH_CLIENT_ID='your-microsoft-application-client-id'
export OAUTH_CLIENT_SECRET='your-microsoft-client-secret'
# Generate once; retain and reuse this value with the same database.
export ENCRYPTION_KEY="$(openssl rand -base64 32)"

cd outlook # From the repository root.
docker compose up --build -d
docker compose logs -f oauth-proxy
```

Keep the credentials and encryption key outside version control. Re-export the same key in new terminal sessions; generating a replacement prevents decryption of saved tokens.

Connect MCP Inspector or another OAuth-capable MCP client using Streamable HTTP at `http://localhost:8080/mcp`, then complete Microsoft sign-in and call a tool. Compose sets the backend's `MCP_PATH` to `/mcp` and the proxy's `MCP_SERVER_URL` to the pathless origin `http://app:9000`. Running Outlook directly retains the default `/mcp/outlook` endpoint unless you set `MCP_PATH`.

The stack uses host ports 8080, 9000, and 5432 and the PostgreSQL container name `oauth_db`; stop any conflicting local stacks first. Stop this stack with `docker compose down`; its database volume remains available for the next run. Avoid `down -v` unless you intend to delete the saved OAuth data.
