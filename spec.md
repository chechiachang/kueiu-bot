# Kueiu Bot

## Constitution

Prefer the least code, comments, and documentation that solve the problem. Less is better; avoid unnecessary abstractions and features.

## Product

A Go Telegram bot backed by an Azure OpenAI agentic LLM. The agent can read and write records in a configured Notion database using a Notion personal access token (PAT).

## Telegram interaction

- Accept requests from users in all Telegram chat types, including private chats, groups, supergroups, and channels. Handle channel posts as well as regular messages.
- Treat user messages and identifiable user-authored channel posts as requests without requiring a bot mention. If a Telegram mention entity for the bot is present, detect it as an entity rather than by substring matching.
- When a message includes the bot's mention entity, remove it and send only the remaining request to the agent.
- When a request is a reply, send the replied-to message and the request, clearly labeled as quoted context and user request.
- Ignore bot-authored messages and a bare mention with no request or replied-to content.
- Treat every request as independent. Do not include earlier messages or retain conversation memory.
- Ignore other messages, including bot messages.
- Reply in the originating chat and report tool failures without claiming success.

## Implementation

- Use Cobra (`github.com/spf13/cobra`) for the CLI, Viper (`github.com/spf13/viper`) for config, and `github.com/joho/godotenv` to load `.env`; start with a `serve` command.
- Use Logrus (`github.com/sirupsen/logrus`) for structured logs. Never log credentials, full prompts, or Notion page content.
- Use `github.com/go-telegram/bot` with long polling for messages and channel posts. Reply in the originating chat or topic where Telegram permits sending messages.
- Use the official OpenAI Go SDK (`github.com/openai/openai-go`) with Azure OpenAI's Responses API and function tools. Make each request stateless: do not pass a prior response/conversation ID or store a local transcript. Confirm the Azure endpoint/deployment supports the Responses API and tool calling.
- Execute tool calls in a bounded loop, return tool results to the model, and honor request cancellation.

## Notion agent tools

Use a private Notion integration in one intended workspace. Allow workspace-wide search and reads only within that integration's workspace access; scope writes to the configured database. Expose only the operations the agent needs:

- Inspect the database/data-source schema.
- Query database pages with property filters, sorts, and pagination; retrieve a page's properties and content blocks.
- Search accessible workspace pages by title when the target is unclear. Notion's search API is title-oriented, not full-text search; use database queries for property-based lookup.
- Create a database page, update its properties, and append or update page content.
- Return page IDs and URLs from tool results so the agent can identify and link to changed pages.

Useful later, if needed: add comments, retrieve users for people properties, and archive pages. Do not expose database schema changes or destructive operations by default.

## Configuration

Use `config.yaml` for non-secret defaults and `.env` for local credentials; provide `.env.example` with empty/sample values. Load `.env` into the process environment, map nested Viper keys to underscore-separated environment variables, then resolve flags, environment, config file, and defaults in that order. Configure the Telegram bot token, Azure OpenAI endpoint/deployment/API key, and Notion PAT/database ID. Keep `.env` ignored by git and share the target database with the private Notion integration.
