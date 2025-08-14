# Weather Plugin

Build the plugin (.so) and drop it into the chatbot UI.

```bash
cd plugins/weather
go build -buildmode=plugin -o weather.so .
```

Then in the web UI, click “Load Plugin” and select `weather.so`.
