# Composite Groups

Composite groups are customer-facing routing, quota, subscription, and usage
containers. They do not own an account pool or a default billing multiplier.
Every billable model must match an enabled route that points to one active,
concrete target group.

## Runtime Contract

For each Composite request:

1. Match the requested model and endpoint against an enabled route.
2. Reject the request when no route matches.
3. Schedule accounts only from the route's `target_group_id`.
4. Price the request using the route multiplier override, or the target
   group's multiplier when the override is empty.
5. Keep quota, subscription usage, deductions, and usage-log ownership on the
   Composite parent group.

There is no provider detector fallback and no Composite-group fallback
multiplier. Composite-specific user multipliers are unsupported.

## Route Registry

Admins configure routes from the group list's `Routes` action or through:

- `GET /api/v1/admin/groups/:id/composite-routes`
- `POST /api/v1/admin/groups/:id/composite-routes`
- `PUT /api/v1/admin/groups/:id/composite-routes/:route_id`
- `DELETE /api/v1/admin/groups/:id/composite-routes/:route_id`
- `POST /api/v1/admin/groups/:id/composite-routes/preview`

Each route contains:

- `public_model`: exact model identifier or prefix accepted from clients.
- `match_type`: `exact` or `prefix`.
- `target_group_id`: required active concrete-provider group.
- `target_platform`: denormalized from the target group for compatibility and
  display; clients do not choose it independently.
- `rate_multiplier`: optional route override; empty inherits the target group.
- `upstream_model`: optional model rewrite. Empty preserves the requested
  model. Exact routes normalize an empty value to `public_model`; prefix routes
  should normally leave it empty.
- `endpoint`: `any`, `messages`, `count_tokens`, `responses`,
  `chat_completions`, `embeddings`, `images`, or `gemini`.
- `priority`: lower values win after match specificity.
- `enabled`: disabled routes remain visible to admins but never match.

Resolution prefers exact over prefix, endpoint-specific over `any`, longer
prefixes over shorter prefixes, lower priority, then lower route ID.

The public `/v1/models` response is also route-bound. Exact routes expose their
public alias. Prefix routes expose matching models available from their target
group. Models without a route are not advertised.

## Setup Example

To sell one subscription that supports OpenAI and Claude:

1. Keep separate active OpenAI and Anthropic groups with their own account
   pools and base multipliers.
2. Create a `composite` group with subscription type `subscription`.
3. Add `gpt` as a prefix route to the OpenAI group, leaving
   `upstream_model` empty.
4. Add `claude` as a prefix route to the Anthropic group, leaving
   `upstream_model` empty.
5. Set an optional multiplier on either route only when it should override the
   target group's multiplier.
6. Bind the subscription plan to the Composite group.

The Composite group itself should have no directly assigned or copied
accounts. Account availability and provider-specific pricing policy come from
each route's target group.

## Limits

Composite routes do not create model metadata, channel prices, or provider
capabilities. Keep those configured on the concrete target groups and their
accounts.

Composite routing does not provide:

- Automatic provider selection for an abstract task.
- Detector-based fallback for unconfigured models.
- Nested Composite targets.
- A default multiplier that applies when no route matches.
