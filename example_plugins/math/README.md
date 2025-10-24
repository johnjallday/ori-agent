# Math Plugin

Build the plugin (.so) and drop it into the chatbot UI.

```bash
cd plugins/math
go build -buildmode=plugin -o math.so .
```

Then in the web UI, click "Load Plugin" and select `math.so`.
