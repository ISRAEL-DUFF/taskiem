// You's pages opened from a link (docs/spec/taskiem-for-everyone.md §7;
// the server's appPaths: /you, /you/chats/*, /you/orders[/*],
// /you/discover[/*]): on You, its tab and the screen over it; in a
// business's session (a business session is its person too), the screen
// pushed over the business's home.

import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import '../app/route.dart';
import '../chat/discover.dart' show DiscoverProfileScreen, DiscoverTab;
import '../chat/my_chat.dart';
import '../chat/your_orders.dart' show YourOrderPage, YourOrdersTab, aboutOrderDraft;
import '../kit/mobile.dart' show tkPush;

/// You's tabs, in their order.
const youTabChats = 0, youTabOrders = 1, youTabDiscover = 2;

/// Opens [r]: [showTab] brings one of You's tabs to the front (null in a
/// business's session: Orders and Discover are pushed instead); [open]
/// opens a chat by its id.
Future<void> openYouPage(BuildContext context, Auth auth, YouRoute r, {required OpenMyChat open, void Function(int tab)? showTab, DateTime Function() now = DateTime.now}) async {
  switch (r.page) {
    case YouPage.chats:
      showTab?.call(youTabChats);
    case YouPage.chat:
      showTab?.call(youTabChats);
      await open(r.id);
    case YouPage.newChat:
      showTab?.call(youTabChats);
      final order = r.ref.isNotEmpty;
      await chatWithBusiness(context, auth, open, business: r.id, from: order ? 'order' : 'discover', draft: order ? aboutOrderDraft(r.ref) : '', tool: r.tool);
    case YouPage.orders:
      if (showTab != null) return showTab(youTabOrders);
      await tkPush<void>(context, YourOrdersTab(auth: auth, onOpenChat: open, now: now, back: true));
    case YouPage.order:
      showTab?.call(youTabOrders);
      await tkPush<void>(context, YourOrderPage.of(auth: auth, business: r.id, reference: r.ref, onOpenChat: open, now: now));
    case YouPage.discover:
      if (showTab != null) return showTab(youTabDiscover);
      await tkPush<void>(context, DiscoverTab(auth: auth, onOpenChat: open, back: true));
    case YouPage.business:
      showTab?.call(youTabDiscover);
      await tkPush<void>(context, DiscoverProfileScreen(auth: auth, business: r.id, onOpenChat: open));
  }
}
