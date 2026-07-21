# calcmsfeeder

`calcmsfeeder` is an interactive CLI that finds configured recurring events in
calCMS and uploads the corresponding stream file to each matching event.

The program always shows the selected date range, matching event IDs, and files
before making changes. Uploads only begin after confirmation with `y`.

## Requirements

- Go version declared in `go.mod`
- An HTTPS calCMS instance and valid planning credentials
- One readable, regular upload file for every configured series

## Configuration

Copy `.env-sample` to `.env`, then set the credentials and series mappings.
`.env` is ignored by Git and must not be committed.

```dotenv
CALCMS_HOST="https://programm.example.org/"
CALCMS_USER="planning-user"
CALCMS_PASS="change-me"
CALCMS_PROJECT_ID=1
CALCMS_STUDIO_ID=1
SERIES_FILES="Morning Show:./uploadfiles/morning.stream,Evening Show:./uploadfiles/evening.stream"
SERIES_IDS="Morning Show:395,Evening Show:404"
DEFAULT_DURATION_IN_DAYS=7
MAX_DURATION_IN_DAYS=30
CALCMS_REQUEST_TIMEOUT=5m
```

`SERIES_FILES` and `SERIES_IDS` must contain exactly the same keys. Relative
upload paths are resolved relative to the selected configuration file. The
application rejects missing files, missing IDs, insecure HTTP hosts, empty
credentials, and invalid duration settings before contacting calCMS.
`CALCMS_REQUEST_TIMEOUT` limits each complete HTTP request, including an upload,
and accepts Go duration values such as `30s`, `5m`, or `1h`.

## Run

```sh
go run .
```

To select another environment file:

```sh
go run . -config.file ./config/production.env
```

If an event already has an active recording, it is skipped by default. Use the
explicit overwrite flag to upload a replacement and make it the active
recording. calCMS retains the previous recording as an inactive entry:

```sh
go run . -overwrite
```

Enter a start date and an inclusive duration. For example, seven days starting
on `2026-07-21` processes `2026-07-21` through `2026-07-27`. Press Enter to use
today and the configured default duration.

The command stops on query, authentication, or upload errors. Some earlier
events may already have been updated if a later upload fails; rerun the command
and review the displayed event IDs before confirming again.

## Development

```sh
go test -race ./...
go vet ./...
```
