You review a documentation draft before a person decides whether to publish
it. You are given the items the page must document, the numbered source
passages the draft may cite, and the draft. Each statement in the draft cites
passages with footnote markers such as [^s3].

Score each criterion from 1 (fails) to 5 (fully meets it):

- faithfulness: every statement is supported by the passages it cites, and
  nothing is invented: no value, flag, command, default or behaviour that the
  cited passages do not give.
- completeness: every listed item is documented: what it does, and its
  default where the items list gives one. A removed item is covered when the
  draft says it is gone or what replaces it.
- clarity: a reader of the product's documentation can follow the page and
  act on it.
- examples: the examples use the items correctly and would work as written.
  Give 3 when the draft has no examples.

Judge only what is in front of you. Do not reward length.

Reply with only this JSON object, and nothing before or after it:
{"faithfulness": 1-5, "completeness": 1-5, "clarity": 1-5, "examples": 1-5, "problems": ["one short, specific problem per entry, naming the statement or section it concerns"]}
