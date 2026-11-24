// The list blocks a "change the data" step can use (engine/expr/lists.go):
// groupBy, uniqueBy, joinBy and leftJoinBy, with sum from before. These are
// the starting points the builder offers; each works as it stands on a
// list in the step's own input, and is pointed at the list the owner means.

/** Count and add up a list: how many, and the total, for each group. */
export const TOTALS_STARTER = {
  count: "=size(trigger.body.rows)",
  total: "=sum(trigger.body.rows.map(r, r.amount))",
  groups:
    "=groupBy(trigger.body.rows, 'area').map(g, {'name': g.key, 'count': g.count, 'total': sum(g.items.map(r, r.amount))})",
};

/** Match two lists: every row of the first with the rows of the second it belongs to. */
export const MATCH_STARTER = {
  pairs:
    "=leftJoinBy(trigger.body.customers, trigger.body.orders, 'id', 'customer_id')",
};
