// You's pages opened from a link (docs/spec/taskiem-for-everyone.md §7;
// the server's appPaths: /you, /you/chats/*, /you/orders[/*],
// /you/discover[/*]): on You, its tab and the screen over it; in a
// business's session (a business session is its person too), the screen
// pushed over the business's home.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import '../app/route.dart';
import '../chat/discover.dart' show DiscoverProfileScreen, DiscoverTab;
import '../chat/group_chat.dart' show GroupChatData, GroupChatScreen;
import '../chat/groups.dart' show OpenGroup;
import '../chat/my_chat.dart';
import '../chat/people_chat.dart' show PeopleChatData, PeopleChatScreen;
import '../chat/people_find.dart' show OpenPeopleChat, PeopleRequestsScreen;
import '../chat/people_standing.dart' show YourStandingScreen;
import '../chat/your_orders.dart' show YourOrderPage, YourOrdersTab, aboutOrderDraft;
import '../kit/mobile.dart' show tkPush;

/// You's tabs, in their order.
const youTabChats = 0, youTabOrders = 1, youTabDiscover = 2;

/// Opens [r]: [showTab] brings one of You's tabs to the front (null in a
/// business's session: Orders and Discover are pushed instead); [open]
/// opens a chat with a business by its id, [openPerson] one with a person
/// (pushed here when not given).
Future<void> openYouPage(
  BuildContext context,
  Auth auth,
  YouRoute r, {
  required OpenMyChat open,
  OpenPeopleChat? openPerson,
  OpenGroup? openGroup,
  void Function(int tab)? showTab,
  DateTime Function() now = DateTime.now,
}) async {
  final person = openPerson ?? (id, {started}) => tkPush<void>(context, PeopleChatScreen(data: PeopleChatData(auth, id, now: now, started: started), openBusiness: open));
  final group = openGroup ?? (id, {focus}) => tkPush<void>(context, GroupChatScreen(data: GroupChatData(auth, id, now: now, focus: focus), openBusiness: open, openPerson: person));
  switch (r.page) {
    case YouPage.group:
      showTab?.call(youTabChats);
      await group(r.id);
    case YouPage.groupInvites:
      // Requests holds the invites to groups.
      showTab?.call(youTabChats);
      await tkPush<void>(context, PeopleRequestsScreen(auth: auth, openChat: person, openGroup: group, now: now));
    case YouPage.person:
      showTab?.call(youTabChats);
      await person(r.id);
    case YouPage.requests:
      showTab?.call(youTabChats);
      await tkPush<void>(context, PeopleRequestsScreen(auth: auth, openChat: person, openGroup: group, now: now));
    case YouPage.request:
      // Requests, and the request over it: answered, Requests is there.
      showTab?.call(youTabChats);
      unawaited(tkPush<void>(context, PeopleRequestsScreen(auth: auth, openChat: person, openGroup: group, now: now)));
      await person(r.id);
    case YouPage.standing:
      showTab?.call(youTabChats);
      await tkPush<void>(context, YourStandingScreen(auth: auth, now: now));
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
