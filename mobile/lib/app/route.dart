// Where a page of the app's web address opens in the app (a tapped
// notification, a link: docs/spec/mobile-app-m4.md N4, N5): the tab it
// belongs to, and the screen over it, if any.

/// What opens over the tab.
enum RouteScreen { none, conversation, customer, taskiem, invoice, bill, purchase, tradeOrder, storefrontOrder }

/// The tabs: Chats, Today, Money, Orders.
const tabChats = 0, tabToday = 1, tabMoney = 2, tabOrders = 3;

typedef AppRoute = ({int tab, RouteScreen screen, String id});

/// The route of [path] (a path with at most its question).
AppRoute routeOf(String path) {
  final uri = Uri.tryParse(path);
  if (uri == null) return (tab: tabToday, screen: RouteScreen.none, id: '');
  final p = uri.path;
  if (p == '/chat') {
    final with_ = uri.queryParameters['with'];
    if (with_ == null || with_.isEmpty) return (tab: tabChats, screen: RouteScreen.none, id: '');
    if (with_.startsWith('c-')) return (tab: tabChats, screen: RouteScreen.customer, id: with_.substring(2));
    return (tab: tabChats, screen: RouteScreen.conversation, id: with_);
  }
  if (p == '/approvals' || p.startsWith('/approvals/')) return (tab: tabChats, screen: RouteScreen.taskiem, id: '');
  final one = RegExp(r'^/(invoices|bills|purchases|trade-orders|storefront/orders)/([^/]+)$').firstMatch(p);
  if (one != null) {
    final kind = one[1]!;
    final tab = kind == 'invoices' || kind == 'bills' ? tabMoney : tabOrders;
    final String id;
    try {
      id = Uri.decodeComponent(one[2]!);
    } on FormatException {
      return (tab: tab, screen: RouteScreen.none, id: '');
    }
    final screen = switch (kind) {
      'invoices' => RouteScreen.invoice,
      'bills' => RouteScreen.bill,
      'purchases' => RouteScreen.purchase,
      'trade-orders' => RouteScreen.tradeOrder,
      _ => RouteScreen.storefrontOrder,
    };
    return (tab: tab, screen: screen, id: id);
  }
  if (p.startsWith('/trade-orders') || p.startsWith('/storefront/orders') || p.startsWith('/purchases')) {
    return (tab: tabOrders, screen: RouteScreen.none, id: '');
  }
  if (p.startsWith('/bills') || p.startsWith('/invoices') || p.startsWith('/money')) return (tab: tabMoney, screen: RouteScreen.none, id: '');
  return (tab: tabToday, screen: RouteScreen.none, id: '');
}
