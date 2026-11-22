// Display aliases apply only to reviewed interface keys, never records or wire data.
export const previewEnglish: Record<string, string> = {
  "OpenAI fixture": "OpenAI test",
  "I reviewed this research wire and consent to one fixture request":
    "I reviewed this research wire and consent to one test request",
  "I reviewed this wire and its allowed local reads and consent to one fixture request":
    "I reviewed this wire and its allowed local reads and consent to one test request",
  "Run research fixture": "Run research test",
  "OpenAI fixture ·": "OpenAI test ·",
  "Unlocked (disposable fixture)": "Unlocked (test only)",
  "(fixture only)": "(test only)",
  "Add fixture news source": "Add test news source",
  "Advanced AI schedules · fixtures": "Advanced AI schedules · tests",
  "All three API operations are simulated in this build. Test credentials only; real calls stay disabled. The compiled fixture returns a simulated count of 256 tokens, not a local tokenizer.":
    "All three API operations are simulated in this build. Test credentials only; real calls stay disabled. The test returns a simulated count of 256 tokens, not a local tokenizer.",
  "An approval receipt records owner consent for the fixture. It does not enable real wallets, automatic unlock or live trading.":
    "An approval receipt records owner consent for this test. It does not enable real wallets, automatic unlock or live trading.",
  "Approve this context, source policy and one fixture run":
    "Approve this context, source policy and one test run",
  "Authorize a fixed context and a bounded schedule. Each successful, validated fixture response allows the selected reminder or fixed paper evaluation. Cards cannot change inputs or execute trades. Changed context, expired consent or provider failure holds this task; independent local tasks continue.":
    "Authorize a fixed context and a bounded schedule. Each successful, validated test response allows the selected reminder or fixed paper evaluation. Cards cannot change inputs or execute trades. Changed context, expired consent or provider failure holds this task; independent local tasks continue.",
  "Authorize fixture schedule": "Authorize test schedule",
  "Current fixture preview · retrieved": "Current preview · retrieved",
  "DEVELOPMENT FIXTURES": "TEST ONLY",
  "Execution is disabled in this core. Only an explicitly started local development fixture can run.":
    "Execution is disabled in this core. Only an explicitly started local test can run.",
  FIXTURE: "TEST",
  "Fetch fixture headlines": "Fetch test headlines",
  "Fixture account": "Test account",
  "Fixture bot identity": "Test bot identity",
  "Fixture budget (micro USD)": "Test budget (micro USD)",
  "Fixture cost: spent": "Test cost: spent",
  "Fixture model": "Test model",
  "Fixture news source": "Test news source",
  "Fixture ·": "Test ·",
  "I reviewed this context and these fixture limits":
    "I reviewed this context and these test limits",
  "Local fixture": "Local test",
  "Manually unlocked fixture. Real execution is disabled.":
    "Manually unlocked for testing. Real execution is disabled.",
  "No exchange, wallet, live price or return calculation is connected. These examples exercise budgets and restart handling using integer fixture units.":
    "No exchange, wallet, live price or return calculation is connected. These examples exercise budgets and restart handling using integer test units.",
  "One fixture run · news age ≤": "One test run · news age ≤",
  "Only a standalone core explicitly started with the development fixture can activate or run AI schedules.":
    "Only a standalone core explicitly started in test mode can activate or run AI schedules.",
  "Refresh fixture reference": "Refresh test reference",
  "Reserve enough for every evaluation at the reviewed limits. Up to ten retained/future requests share local capacity. Prices and token counts are fixtures; no real API is connected.":
    "Reserve enough for every evaluation at the reviewed limits. Up to ten retained/future requests share local capacity. Prices and token counts are simulated; no real API is connected.",
  "Run development fixture": "Run test",
  "Run fixture digest once": "Run test digest once",
  "Select a current fixture reference": "Select a current test reference",
  "The local paper journey uses public fixtures. Live WEN integration awaits independent contract acceptance.":
    "The local paper journey uses public examples. Live WEN integration awaits independent contract acceptance.",
  "The run fetches fresh fixture headlines under this source policy. Headlines are untrusted data.":
    "The run fetches fresh test headlines under this source policy. Headlines are untrusted data.",
  "To automate, select a source in Tasks. Each run fetches new evidence before its reminder, paper evaluation or optional fixture AI step. Unavailable/stale news holds dependent work. Other tasks and recovery continue. Retained snapshots are bounded and are not silently deleted.":
    "To automate, select a source in Tasks. Each run fetches new evidence before its reminder, paper evaluation or optional test AI step. Unavailable/stale news holds dependent work. Other tasks and recovery continue. Retained snapshots are bounded and are not silently deleted.",
  "Total fixture budget": "Total test budget",
  "Waiting for the fixture result.": "Waiting for the test result.",
  "fixture budget": "test budget",
  "fixture units": "test units",
  "fixture units.": "test units.",
  "· Destination fixture:treasury": "· Destination: test treasury",
  "· admission fixture only": "· admission test only",
  "Fixture allowance": "Test allowance",
  Fixture: "Test",
  "fixture-openai-v1": "OpenAI test",
};

// A separate display projection keeps catalog option values and receipts exact.
export function previewIdentity(value: string): string {
  const labels: Record<string, string> = {
    "fixture-4h": "4-hour test",
    "fixture-12h": "12-hour test",
    "fixture-4h-unknown": "4-hour test · uncertain result",
    "fixture-4h-stale": "4-hour test · stale input",
    "fixture-4h-cutoff": "4-hour test · cutoff",
    "fixture-wallet:alpha": "Test pool Alpha",
    "fixture-wallet:beta": "Test pool Beta",
  };
  return Object.hasOwn(labels, value) ? labels[value] : value;
}

export function previewModel(
  value: string,
  text: (key: string) => string,
): string {
  return value === "fixture-openai-v1" || value === "OpenAI fixture"
    ? text(value)
    : value;
}
