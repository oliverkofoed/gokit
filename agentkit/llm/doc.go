// Package llm is the stateless model-call layer of agentkit: plain-data
// Models, tagged-struct Messages, and a Client that streams one completion
// per call across four wire protocols (Anthropic Messages, OpenAI Responses,
// OpenAI Chat Completions, Google Gemini) implemented directly on the
// transport subpackage — no provider SDKs.
//
// Every byte leaves the process through transport.Interface, which is what
// makes the cassette subpackage's record/replay total: swap the transport and
// the whole library runs against recorded traffic. The llmtest subpackage
// provides a scripted in-memory Streamer for loop-level tests.
//
// The stateful agent loop lives in the parent package
// github.com/oliverkofoed/gokit/agentkit. See SPEC.md for the full contract; rule
// references like R6 in doc comments point there.
package llm
