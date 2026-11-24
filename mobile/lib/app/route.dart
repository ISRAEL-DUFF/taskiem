// Where a page of the app's web address opens in the app (a tapped
// notification, a link: docs/spec/mobile-app-m4.md N4, N5): the tab it
// belongs to, and the screen over it, if any.

/// What opens over the tab.
enum RouteScreen { none, conversation, customer, taskiem, invoice, bill, purchase, tradeOrder, storefrontOrder, you, youPage, payments, paymentBatch, payroll, pages }

/// A page of You (taskiem-for-everyone §7) a link opens.
enum YouPage { chats, chat, newChat, orders, order, discover, business, person, requests, request, standing, group, groupInvites }

/// A page of You: [id] is the chat (a business's, or a person's, a
/// request's: people-chatting §7), the business (an order's, a profile's,
/// a chat's to start); [ref] an order's reference; [tool] Their shop or
/// Book over a chat started.
typedef YouRoute = ({YouPage page, String id, String ref, String? tool});

/// The page of You [path] is (the web's /you addresses, as appPageOf
/// passes them on), or null when it is not one.
YouRoute? youRouteOf(String path) {
  try {
    return _youRouteOf(path);
  } on Object {
    // A link spelled wrong (a bad %): not a page of You.
    return null;
  }
}

YouRoute? _youRouteOf(String path) {
  final uri = Uri.tryParse(path);
  if (uri == null || !(uri.path == '/you' || uri.path.startsWith('/you/'))) return null;
  final List<String> parts;
  try {
    parts = uri.pathSegments.where((s) => s.isNotEmpty).toList();
  } on FormatException {
    return null;
  }
  if (parts.isEmpty || parts[0] != 'you') return null;
  final ok = RegExp(r'^[A-Za-z0-9\-]{1,80}$');
  String? part(int i) => parts.length > i && ok.hasMatch(parts[i]) ? parts[i] : null;
  YouRoute r(YouPage p, [String id = '', String ref = '', String? tool]) => (page: p, id: id, ref: ref, tool: tool);
  switch (parts.length > 1 ? parts[1] : '') {
    case '':
      return parts.length == 1 ? r(YouPage.chats) : null;
    case 'chats':
      if (parts.length == 2) return r(YouPage.chats);
      if (parts.length != 3) return null;
      if (parts[2] == 'new') {
        final q = uri.queryParameters;
        final business = q['business'] ?? '';
        if (!ok.hasMatch(business)) return r(YouPage.chats);
        final ref = q['ref'] ?? '';
        final tool = q['tool'];
        return r(YouPage.newChat, business, ok.hasMatch(ref) ? ref : '', tool == 'shop' || tool == 'book' ? tool : null);
      }
      final id = part(2);
      return id == null ? r(YouPage.chats) : r(YouPage.chat, id);
    // A chat with a person (/you/people/{chat}) and Requests
    // (/you/people/requests[/{chat}]): a push of kind person or request.
    case 'people':
      if (parts.length == 2) return r(YouPage.chats);
      if (parts[2] == 'requests') {
        if (parts.length == 3) return r(YouPage.requests);
        final id = parts.length == 4 ? part(3) : null;
        return id == null ? r(YouPage.requests) : r(YouPage.request, id);
      }
      if (parts.length != 3) return null;
      final id = part(2);
      return id == null ? r(YouPage.chats) : r(YouPage.person, id);
    // A group (/you/groups/{id}, a push of kind group) and the invites
    // waiting (/you/groups/invites, a push of kind group_invite).
    case 'groups':
      if (parts.length == 2) return r(YouPage.chats);
      if (parts.length != 3) return null;
      if (parts[2] == 'invites') return r(YouPage.groupInvites);
      final id = part(2);
      return id == null ? r(YouPage.chats) : r(YouPage.group, id);
    // Your standing with Taskiem's moderators (a push of kind you, tag
    // standing: people-chatting, "safety and moderation").
    case 'settings':
      if (parts.length == 3 && parts[2] == 'standing') return r(YouPage.standing);
      return r(YouPage.chats);
    case 'orders':
      if (parts.length == 2) return r(YouPage.orders);
      if (parts.length != 4) return null;
      final business = part(2), ref = part(3);
      return business == null || ref == null ? r(YouPage.orders) : r(YouPage.order, business, ref);
    case 'discover':
      if (parts.length == 2) return r(YouPage.discover);
      if (parts.length != 3) return null;
      final business = part(2);
      return business == null ? r(YouPage.discover) : r(YouPage.business, business);
  }
  return null;
}

/// A pages route's id as the page's name and the filters its link set
/// ("disks?filters={…}"); the filters are read as applinks reads them.
({String name, Map<String, String>? filters}) pageRouteOf(String id, Map<String, String>? Function(String?) filtersOf) {
  final uri = Uri.tryParse(id);
  if (uri == null) return (name: id, filters: null);
  return (name: uri.path, filters: filtersOf(uri.queryParameters['filters']));
}

/// The tabs: Chats, Today, Money, Orders.
const tabChats = 0, tabToday = 1, tabMoney = 2, tabOrders = 3;

typedef AppRoute = ({int tab, RouteScreen screen, String id});

/// The route of [path] (a path with at most its question).
AppRoute routeOf(String path) {
  final uri = Uri.tryParse(path);
  if (uri == null) return (tab: tabToday, screen: RouteScreen.none, id: '');
  final p = uri.path;
  // The person's own chat with a business (a push on You): over the chats,
  // in whichever session is open (a business session is its person too).
  final you = youRouteOf(path);
  if (you?.page == YouPage.chat) return (tab: tabChats, screen: RouteScreen.you, id: you!.id);
  // The rest of You (Orders, Discover, a chat to start) over the chats;
  // the id is the path, read again where it opens.
  if (you != null) return (tab: tabChats, screen: you.page == YouPage.chats ? RouteScreen.none : RouteScreen.youPage, id: you.page == YouPage.chats ? '' : path);
  if (p == '/chat') {
    final with_ = uri.queryParameters['with'];
    if (with_ == null || with_.isEmpty) return (tab: tabChats, screen: RouteScreen.none, id: '');
    if (with_.startsWith('c-')) return (tab: tabChats, screen: RouteScreen.customer, id: with_.substring(2));
    return (tab: tabChats, screen: RouteScreen.conversation, id: with_);
  }
  if (p == '/approvals' || p.startsWith('/approvals/')) return (tab: tabChats, screen: RouteScreen.taskiem, id: '');
  // A batch to check and pay (the link Taskiem sends in the chat for a
  // list pasted there): Payments over Money, the batch over it.
  final batch = RegExp(r'^/payments/batches/([^/]+)$').firstMatch(p);
  if (batch != null) {
    try {
      return (tab: tabMoney, screen: RouteScreen.paymentBatch, id: Uri.decodeComponent(batch[1]!));
    } on FormatException {
      return (tab: tabMoney, screen: RouteScreen.payments, id: '');
    }
  }
  // Payments, and one payment to confirm (the link Taskiem sends in the
  // chat): the list over Money, the payment over it; id '' for the list.
  final pay = RegExp(r'^/payments(?:/([^/]+))?$').firstMatch(p);
  if (pay != null) {
    try {
      return (tab: tabMoney, screen: RouteScreen.payments, id: pay[1] == null ? '' : Uri.decodeComponent(pay[1]!));
    } on FormatException {
      return (tab: tabMoney, screen: RouteScreen.payments, id: '');
    }
  }
  // Payroll (Payrolla's runs), and one run (/payroll/runs/{run}): over
  // Money; id '' for the list. Staff in Payrolla opens the list too.
  final payroll = RegExp(r'^/payroll(?:/runs/([^/]+)|/staff)?$').firstMatch(p);
  if (payroll != null) {
    try {
      return (tab: tabMoney, screen: RouteScreen.payroll, id: payroll[1] == null ? '' : Uri.decodeComponent(payroll[1]!));
    } on FormatException {
      return (tab: tabMoney, screen: RouteScreen.payroll, id: '');
    }
  }
  // Pages (decision 0072): the list, and one page by its short name, over
  // Today; id '' for the list. A page's editor is the web's.
  // A page link's filters (a Tell me message's page button, pages H5) go
  // with its name: "disks?filters=…", read where it opens (pageRouteOf).
  final pages = RegExp(r'^/pages(?:/([a-z][a-z0-9-]{1,23}))?$').firstMatch(p);
  if (pages != null) {
    final name = pages[1] ?? '';
    final f = uri.queryParameters['filters'];
    return (tab: tabToday, screen: RouteScreen.pages, id: name.isNotEmpty && f != null ? Uri(path: name, queryParameters: {'filters': f}).toString() : name);
  }
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
