# Running Ornith models

Ornith is a family of open-weight coding models from DeepReinforce, released
under the MIT license. Version 1.5 comes as a 9B dense model, a 35B mixture of
experts (35B-A3B, about 3B active) and a 397B mixture of experts. The model's
source and serving notes are at https://github.com/deepreinforce-ai/Ornith-1.

Flockdeck has no Ornith-specific support. Ornith is served behind an
OpenAI-compatible API with tool calling, and that is what the
OpenAI-compatible endpoint agent talks to (see [Agents and models](#agents)).
This setup has been checked against a stand-in OpenAI-compatible endpoint, not
a live Ornith server. A slash in the model id, tool offering, the reasoning
stream and a tool-call round trip worked there. Tool calling and status
detection with a real Ornith model are untested.

## What the server has to do

The chat client sends tools with every request and reads the reasoning stream
from `reasoning_content` or `reasoning`. If the server does not parse these,
tool calls and thinking arrive as plain text in the answer and the agent cannot
act on them. Ornith's README serves it with both parsers on. For vLLM
(0.19.1 or later, per the README) that is:

```sh
vllm serve ornith-ai/Ornith-1.5-9B \
    --served-model-name Ornith-1.5-9B \
    --port 8000 \
    --enable-auto-tool-choice --tool-call-parser qwen3_xml \
    --reasoning-parser qwen3 \
    --trust-remote-code
```

The README's recipe includes `--trust-remote-code`; it does not say whether the
flag is required. The 35B-A3B and 397B models need more GPUs, so add
`--tensor-parallel-size` and a context length for your hardware, as the README
does. SGLang (0.5.9 or later) uses `--tool-call-parser qwen3_coder
--reasoning-parser qwen3`. Take the full flags from the README. Its serve
commands name the repository `deepreinforce-ai/...`, but the checkpoints are
published under `ornith-ai/`, so check the id on Hugging Face before serving.

For Ollama, the README gives `ollama run
hf.co/deepreinforce-ai/Ornith-1.0-9B-GGUF`. That is the 1.0 build, not 1.5,
and Ollama may report no tool support for a GGUF pulled this way because the
chat template comes with it. A model Ollama says cannot use tools is retried
without them, and then it cannot edit files or run commands.

The model id you give Flockdeck is whatever the server calls the model: the
`--served-model-name` above, or the id a hosted provider lists. A slash in the id
is sent as it is. `/models` on the endpoint shows what it calls them.

## Using the built-in endpoint agent

Give the OpenAI-compatible endpoint agent the server's address, then choose a
model.

```sh
flockdeck keys endpoint openai-compatible http://127.0.0.1:8000/v1
```

You can do the same from the picker: choose **OpenAI-compatible endpoint** and
type the address. An address on this machine needs no key. For a hosted
provider, use its documented base URL and set the key with
`flockdeck keys set openai-compatible`.

The built-in agent lists no models. In a pane, `/model` lists what the
endpoint's `/models` reports, and `/model <number>` or `/model <id>` switches.
A chat started with no model on a local server that offers exactly one picks
that one.

## Naming the model once

To have Ornith in the picker with a default model, add an agent of your own to
`agents.json` (see [Adding your own](#agents)):

```json
{
  "version": 1,
  "agents": [
    {
      "id": "ornith",
      "name": "Ornith 9B",
      "runner": "api",
      "api": {
        "wire": "openai",
        "baseURL": "http://127.0.0.1:8000/v1"
      },
      "models": [
        { "id": "Ornith-1.5-9B", "name": "Ornith 1.5 9B", "tier": "small" }
      ]
    }
  ]
}
```

One vLLM or SGLang process serves one model, and an agent has one address. To
use a second model, such as the 35B-A3B, run a second server on another port
and add a second entry with its own `id`, `baseURL` and model. For a hosted
provider that serves several models at one address, one entry can list them all.
Leave `keyEnv` out for a server on this machine; for a hosted one add
`"keyEnv": ["ORNITH_API_KEY"]` or set the key with `flockdeck keys set`.

Because the address is not the vendor's own, these panes show tokens and no
dollar cost.

## Limits

Flockdeck does not set sampling parameters. Ornith's README recommends a
temperature of 0.6 for precise coding work and 1.0 for general tasks, with
`top_k` 20. Set those as defaults on the server, since the chat client sends
only the model, messages and tools.

Only Ollama's "does not support tools" error triggers the retry without tools.
vLLM started without `--enable-auto-tool-choice` refuses the request, and the
error shows in the pane.
