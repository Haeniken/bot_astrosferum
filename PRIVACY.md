# Privacy notice for deployments

`bot_astrosferum` is self-hosted software. Each deployment operator is responsible for its own privacy notice, lawful basis, access controls, retention policy, and user-support contact.

The application may process and store:

- platform-namespaced Telegram or VK numeric user identifiers and the chat/peer identifier needed to reply;
- up to ten user-named geographic points;
- daily aggregated successful and failed forecast-request counts;
- transient coordinates needed to produce a forecast.

Tokens and database credentials are read from ignored runtime files or environment variables. The application should not log message bodies, bot tokens, full Telegram/VK updates, or exact user coordinates. Daily usage aggregates are retained for 90 days by the current PostgreSQL maintenance job; saved points remain until the user changes/deletes them or the operator removes the account data. The independent website has its own privacy notice and owns its sessions, preferences, and saved-visualization catalogue; removing that deployment does not remove the bot database or affect bot operation.

The public source repository contains no production database, secrets, user identifiers, or saved user locations. Only explicitly documented, non-secret example locations and the designated public Astrodome fixture may appear in repository or public-site assets.

Before reporting a bug, remove identifiers, tokens, database dumps, exact private locations, and full bot updates. Security-sensitive reports should follow [SECURITY.md](SECURITY.md).
