const express = require("express");
const app = express();
const port = process.env.PORT || 3000;

app.get("/", (_req, res) => res.send("hello from 0ops node-demo\n"));
app.get("/healthz", (_req, res) => res.json({ ok: true }));

app.listen(port, () => console.log("node-demo listening on " + port));
