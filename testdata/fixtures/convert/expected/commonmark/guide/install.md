---
title: Install the Lumen agent
author: Ada Placeholder
description: How to install the agent on a host.
tags: [install, agent]
---

Install Lumen 2.1 on a host. See [Verify the installation](#verify-the-installation) when you finish,
and the [settings reference](configure.md#settings). Press <kbd>Ctrl</kbd>+<kbd>C</kbd> to stop,
then click **Save** or choose **File > Export > PDF**.[^1]

<!-- TODO: add Windows steps -->

## Before you begin

> **Note:** You need **root** access and `curl`.

> **Warning: Data loss:** Upgrading from 1.x deletes the cache.
>
> Back it up first.

This paragraph needs the product attribute.

Enterprise-only text.

## Install

1. Download the package:

   ```bash
   curl -fsSL https://example.test/install.sh -o install.sh # (1)
   sh install.sh --prefix /opt/lumen # (2)
   ```

- (1) Fetch the script.
- (2) Run it.

1. Start the service.
   - Check the status.
   - Read the logs.
1. Done.

<!-- - -->

- [x] Downloaded
- [ ] Started

<!-- - -->

- **Port**: The port to listen on.
- **Host**: The address to bind.

![Architecture diagram](images/diagram.png)

**Supported platforms**

| Platform | Supported |
| --- | :---: |
| Linux | Yes |
| macOS | No |

<table>
<tr>
<td colspan="2">Merged heading</td>
</tr>
<tr>
<td>a</td>
<td>b</td>
</tr>
</table>

<table>
<tr>
<td rowspan="2">Spans two rows</td>
<td>c</td>
</tr>
<tr>
<td>d</td>
</tr>
</table>

> **Tip:** Keep the configuration file in version control.

## Verify the installation

Run the check, as described at [the docs](https://example.test/docs) or [Status page](https://status.example.test) or link:Status page[https://status.example.test].

> It is easier to ask forgiveness than it is to get permission.
>
> — Grace Hopper, Interview

```
literal block
  keeps spacing
```

> Sidebar content.

This is <mark>important</mark> text, E=mc<sup>2</sup> and H<sub>2</sub>O, indexed term, and “quoted”.

<div class="custom">raw html</div>

```text
plain text with a callout
```

- (1) No comment syntax for text.

[^1]: Only on Linux.
