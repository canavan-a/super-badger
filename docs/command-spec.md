# Super Badger command spec

A metric source (a "Super Badger Station Standard API" endpoint) can offer
**commands**: actions such as toggling a model on or off. Commands belong to the
source as a whole, not to any station, and are served from their own endpoint.
The metrics JSON is unchanged.

## Discovery

The commands endpoint is the sibling `commands` path next to the metrics URL:

| Metrics URL                    | Commands URL                    |
|--------------------------------|---------------------------------|
| `http://box:9000/metrics`      | `http://box:9000/commands`      |
| `http://box:9000/`             | `http://box:9000/commands`      |
| `http://box:9000/api/metrics`  | `http://box:9000/api/commands`  |

The server polls it on the same schedule as the metrics, sending the same
`Authorization: Bearer <api_key>` header when the source has a key.

## `GET <commands URL>`

Respond `200` with a JSON array, in the order the commands should be shown:

```json
[
  {"path": "model/toggle", "label": "Toggle model", "url": "/model/toggle"},
  {"path": "restart",      "label": "Restart",      "url": "http://box:9000/restart"}
]
```

| Field   | Required | Meaning                                                              |
|---------|----------|----------------------------------------------------------------------|
| `path`  | yes      | Identifier, unique within this source.                               |
| `label` | no       | Button text in the app. The app shows `path` when it's empty.        |
| `url`   | yes      | Where to POST to run it. Relative URLs resolve against the commands URL. |

Optional display hints:

| Field     | Meaning                                                                      |
|-----------|------------------------------------------------------------------------------|
| `group`   | Section heading. A section starts where its first command appears in the list. |
| `active`  | `true` marks the current state, e.g. the running mode.                        |
| `confirm` | The app shows this text and asks before running the command.                  |
| `options` | `[{"value", "label", "active"}]` makes the command a picker. See below.      |

```json
{"path": "model", "label": "Model", "url": "/commands/model", "group": "Model",
 "options": [{"value": "orca-iq3_xxs", "label": "orca-iq3_xxs", "active": true},
             {"value": "orca-iq4_xs",  "label": "orca-iq4_xs"}]}
```

- Entries missing `path` or `url`, or repeating an earlier `path`, are skipped.
- Options with an empty or repeated `value` are skipped. A picker left with no
  options becomes a plain command.
- The list replaces the previous one on every poll, so a command you stop
  listing disappears within one poll interval.
- Respond `404` if the source has no commands. That clears the list.
- Any other failure keeps the last list the server received.

## Invoking

When the user runs a command, the server sends `POST <url>`. The body is empty
for a plain command. For a picker it's `{"option": "<value>"}` with
`Content-Type: application/json`, and the value is always one the command listed.

- It sends the source's API key only when `url` has the same scheme and host
  as the metrics URL.
- There's no timeout. A command can take as long as it needs (model toggles
  take up to about 60s). It's cancelled if the app disconnects.
- The response status, `Content-Type` and body are streamed back to the app
  as they arrive, so a command can stream progress.
- When a command's response ends, the server re-polls that source's command
  list straight away, so the app's reload shows what the command changed.

### Response (recommended)

Stream `Content-Type: application/x-ndjson`, one JSON event per line:

```
{"log": "starting nixstrata-a (port 8091) ..."}
{"log": "nixstrata-a still loading (60 s) ..."}
{"ok": true, "message": "double up"}
```

- The app shows the latest `log` line while the command runs, then the final
  `message`, marked as an error when `ok` is false.
- Any other content type is shown line by line as plain text.
- Answer `409` with `{"error": "..."}` if another command is still running.
- Keep a command running if the client disconnects. A phone going to sleep
  shouldn't abort a half-finished mode switch.
