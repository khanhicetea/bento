# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

The primary user is a single-host operator checking application health and managing deployments, data, routing, backups, and operations from a browser.

## Product Purpose

Bento is a self-hosted control plane for PHP and HTTP applications on one Linux host. It makes the current state of the stack understandable and lets the operator safely manage it. Success means seeing what needs attention and completing routine tasks without guessing at consequences.

## Positioning

Bento stores desired state in SQLite and reconciles Docker Engine directly. Each application has a persistent container and identity; the backend is not in the application request path.

## Operating Context

Operators connect to the loopback-only management listener, typically through an SSH tunnel. They move between overview, applications, data services, backups, ingress, activity, and system status; operations may be asynchronous.

## Capabilities and Constraints

Preserve the existing REST API, routes, tasks, and exact confirmations. Retained application data and add-only data bindings must not be implied to be automatically removed or reversible. Unknown state is refused, not normalized. Do not suggest the UI is safe to expose on an untrusted network.

## Brand Commitments

Keep the Bento name and existing logo. The requested identity is Japanese-inspired, friendly, minimal, and easy to use. Its Bento name should be visible in the interface's functional box-and-compartment layout, not just in its colors or logo; cultural references should serve usability, not become decorative stereotypes.

## Evidence on Hand

README.md and operator documentation in docs/ describe capabilities and limits. The existing React UI in apps/web/src and the committed logo in apps/web/public/bento-logo-3d.png provide real interface content and visual evidence. No customer testimonials, performance claims, or commercial proof are available.

## Product Principles

- Put health and exceptions before infrastructure jargon.
- Make routine actions easy to find; make consequences clear before disruptive actions.
- Keep state and operation progress near the affected resource.
- Preserve the operator's ability to distinguish intent from observation.
