---
title: Lumen API notes
---

# Lumen API notes

## Workspaces

`GET /v1/workspaces/{id}` returns one workspace with its version and the date
of its last upgrade. It returns 404 when the workspace does not exist.

## Upgrades

`POST /v1/upgrades` starts an upgrade of a workspace and returns 202 with the
upgrade's id. Send the workspace id in the body:

```json
{"workspace": "ws-123", "backup": true}
```

## Server

The API listens on the port in `server.port`, 8080 by default.
