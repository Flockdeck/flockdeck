# Running Ornith models in Flockdeck

Ornith is a family of open-weight coding models from DeepReinforce, released
under the MIT license in 9B, 31B, 35B (mixture of experts) and 397B (mixture of
experts) sizes. Flockdeck needs no Ornith-specific support. Any server that
speaks the OpenAI chat completions API with tool calling works as an API agent,
and Ornith is served that way. The model's source and serving notes are at
https://github.com/deepreinforce-ai/Ornith-1.

This page covers the Flockdeck side. How you host the model is up to you.

## What the server has to do

The chat client sends tools with every request and reads the reasoning stream
from `reasoning_content` or `reasoning`. If the server does not parse these for
you, tool calls and thinking come back as plain text in the answer and the agent
cannot act on them. Ornith's README serves it with both parsers turned on.
For vLLM that is:

```sh
vllm serve deepreinforce-ai/Ornith-1.5-397B \
    --served-model-name Ornith-1.5 \
    --enable-auto-tool-choice --tool-call-parser qwen3_xml \
    --reasoning-parser qwen3
```

SGLang uses `--tool-call-parser qwen3_coder --reasoning-parser qwen3`. The
README also gives an Ollama command (`ollama run
hf.co/deepreinforce-ai/Ornith-1.0-9B-GGUF`) and a `llama-server` one for the 9B
GGUF. Take the full flags, including the tensor-parallel and context-length
settings for your hardware, from the README rather than from here.

The model id you give Flockdeck is whatever the server calls the model: the
`--served-model-name` above (`Ornith-1.5`), the id a hosted provider lists, or
a repository id such as `ornith-ai/Ornith-1.5-9B`. Ids with a slash are sent as
they are. `/models` on the endpoint shows what it calls them.

## Using the built-in OpenAI-compatible agent

Give the agent the server's address, then choose a model.

```sh
flockdeck keys endpoint openai-compatible http://127.0.0.1:8000/v1
```

You can do the same from the picker: choose **OpenAI-compatible endpoint** and
type the address. An address on this machine needs no key. For a hosted
provider, use its documented base URL and set the key with
`flockdeck keys set openai-compatible`.

The built-in agent lists no models. In a pane, `/model` lists what the
endpoint's `/models` reports and `/model <number>` or `/model <id>` switches.
A chat started with no model on a local server that offers exactly one picks
that one.

## Naming the models once

To have Ornith in the picker with its models and a default, add an agent of your
own to `agents.json` (see [Adding your own](../internal/help/pages/agents.md)):

```json
{
  "version": 1,
  "agents": [
    {
      "id": "ornith",
      "name": "Ornith",
      "runner": "api",
      "api": {
        "wire": "openai",
        "baseURL": "http://127.0.0.1:8000/v1",
        "keyEnv": ["ORNITH_API_KEY"]
      },
      "models": [
        { "id": "ornith-ai/Ornith-1.5-9B", "name": "Ornith 1.5 9B", "tier": "small" },
        { "id": "ornith-ai/Ornith-1.5-397B", "name": "Ornith 1.5 397B", "tier": "top" }
      ]
    }
  ]
}
```

The first model listed is the default. Replace the ids with the ones your
server reports. Leave out `keyEnv` for a server on this machine. The tiers let
a routing rule send small jobs to the 9B and hard ones to the 397B.

Because the address is not the vendor's own, Flockdeck shows tokens for these
panes and no dollar cost.

## Limits

Flockdeck does not set sampling parameters. Ornith's README recommends a
temperature of 0.6 for precise coding work and 1.0 for general tasks, with
`top_k` 20. Set those as defaults on the server, since the chat client sends
only the model, messages and tools.

Ollama answering that a model does not support tools makes the pane retry
without them and say so. Other servers do not get that fallback: vLLM started
without `--enable-auto-tool-choice` refuses the request, and the error shows in
the pane.
