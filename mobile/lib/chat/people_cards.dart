// Businesses inside people chats (docs/spec/people-chatting.md §10.1, §11 A):
// a card shared into a chat with a person (a business, one of its products,
// one of your own orders or its delivery), opened live through the customer
// door as the person opening it; and "Your share", a card with the payer's
// bank details and Mark as paid. Taskiem moves no money between people.
// Also: picking people chats to share or forward to, and the bank account
// a share is paid to (`/v1/me/payout-account`).

import 'dart:convert' show jsonEncode;

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/forms.dart' show nairaKobo, TkMoneyField;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading;
import '../kit/load_more.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'cards.dart' show CardButton, CardPill, CardProgress;
import 'door.dart' show deliveryStep, yourOrderStatus, doorTone;
import 'my_chat.dart' show OpenMyChat, chatWithBusiness, sayFailure;
import 'people.dart';
import 'words.dart';

String _str(Object? v) => v is String ? v : '';

/// A card's kind, small over it.
String peopleCardKind(PeopleCard c) => switch (c.type) {
  'product' => 'Product',
  'business' => 'Business',
  'order' => c.title.startsWith('Booking') ? 'Booking' : 'Order',
  'delivery' => 'Delivery',
  'split' => 'Your share',
  'basket' => 'Order together',
  _ => 'Card',
};

/// A bank account in one line: "GTBank · 0123456789 · Ada Obi".
String accountLine(PayoutAccount a) => [a.bank, a.accountNumber, a.accountName].where((s) => s.isNotEmpty).join(' · ');

/// A card in a people chat: what it is, its business, its price or state,
/// a delivery's progress, a share's amount and who to pay; and its
/// buttons (Open, Mark as paid).
class PeopleCardView extends StatelessWidget {
  const PeopleCardView({super.key, required this.card, required this.mine, this.onOpen, this.onPaid, this.busy = false});
  final PeopleCard card;
  final bool mine;

  /// Open it live (not a share).
  final VoidCallback? onOpen;

  /// A share marked paid (true) or not yet (false).
  final ValueChanged<bool>? onPaid;
  final bool busy;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final c = card;
    final split = c.type == 'split';
    final kicker = [peopleCardKind(c), if (!split && (c.business ?? '').isNotEmpty && c.type != 'business') c.business!].join(' · ');
    final paid = c.paid == true;
    final d = c.delivery;
    final to = c.payTo;
    return Semantics(
      container: true,
      label: '${peopleCardKind(c)}: ${c.title}',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  kicker.toUpperCase(),
                  semanticsLabel: kicker,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: tkText(size: 11, weight: 700, letterSpacing: 0.66, color: t.muted),
                ),
              ),
              if (split) ...[const SizedBox(width: 8), CardPill(paid ? 'Paid' : 'Not paid yet', paid ? CardTone.lime : CardTone.warn)],
              if ((c.state ?? '').isNotEmpty && !split) ...[const SizedBox(width: 8), CardPill(yourOrderStatus(c.state!), doorTone(c.state!))],
            ],
          ),
          const SizedBox(height: 6),
          if (split) ...[
            Text(naira(c.amountKobo ?? 0), style: tkDisplay(size: 24, lineHeight: 1.15, letterSpacing: -0.8, color: t.text)),
            if ((c.what ?? '').isNotEmpty) Text('For ${c.what}', style: tkText(size: 13.5, weight: 600, lineHeight: 1.35, color: t.text)),
            if (to != null) ...[
              const SizedBox(height: 6),
              Text(mine ? 'To be paid to you at' : 'Pay to', style: tkText(size: 12.5, color: t.muted)),
              Text(accountLine(to), style: tkText(size: 14, weight: 600, lineHeight: 1.35, color: t.text)),
            ],
            const SizedBox(height: 4),
            Text(
              mine ? 'They pay you by their bank app. Taskiem moves no money.' : 'Pay by your bank app. Taskiem moves no money and does not check payments.',
              style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted),
            ),
          ] else ...[
            Text(c.title, style: tkText(size: 15.5, weight: 600, lineHeight: 1.3, color: t.text)),
            if (c.priceKobo != null) Text(naira(c.priceKobo!), style: tkText(size: 14, weight: 600, color: t.text)),
            if ((c.area ?? '').isNotEmpty) Text(c.area!, style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
            if (d != null) ...[
              const SizedBox(height: 2),
              Text(d.line, style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
              const SizedBox(height: 8),
              CardProgress(steps: deliverySteps, step: deliveryStep(d)),
            ],
          ],
          if (split && onPaid != null) ...[
            const SizedBox(height: 10),
            Row(
              children: [
                if (!mine && to != null) ...[
                  Expanded(
                    child: CardButton(
                      label: 'Copy number',
                      onTap: () async {
                        await Clipboard.setData(ClipboardData(text: to.accountNumber));
                        if (context.mounted) showTkToast(context, 'Account number copied');
                      },
                    ),
                  ),
                  const SizedBox(width: 8),
                ],
                Expanded(child: CardButton(label: paid ? 'Not paid yet' : 'Mark as paid', primary: !paid, busy: busy, onTap: () => onPaid!(!paid))),
              ],
            ),
          ],
          if (!split && onOpen != null) ...[const SizedBox(height: 10), CardButton(label: switch (c.type) { 'business' => 'See the business', 'basket' => 'Open basket', _ => 'Open' }, primary: true, onTap: onOpen)],
        ],
      ),
    );
  }
}

// --- opening a card live ---

/// A card opened live, through the customer door as the person opening it
/// (§10.1): a product's price now, the business's card, an order only when
/// it is theirs. Chat starts their own chat with the business (§10.2).
///
/// [load] reads it another way (a group's card: `GET
/// /v1/me/groups/{id}/messages/{message}/card`).
Future<void> showPeopleCard(BuildContext context, {required Auth auth, required String chat, required String message, required OpenMyChat openBusiness, void Function(String business, String reference)? openOrder, Future<OpenPeopleCardResponse> Function()? load}) =>
    showTkSheet<void>(context, (_) => _LiveCardSheet(auth: auth, chat: chat, message: message, openBusiness: openBusiness, openOrder: openOrder, load: load));

class _LiveCardSheet extends StatefulWidget {
  const _LiveCardSheet({required this.auth, required this.chat, required this.message, required this.openBusiness, this.openOrder, this.load});
  final Future<OpenPeopleCardResponse> Function()? load;
  final Auth auth;
  final String chat;
  final String message;
  final OpenMyChat openBusiness;
  final void Function(String business, String reference)? openOrder;

  @override
  State<_LiveCardSheet> createState() => _LiveCardSheetState();
}

class _LiveCardSheetState extends State<_LiveCardSheet> {
  OpenPeopleCardResponse? _r;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = await (widget.load ?? () => widget.auth.api.openPeopleCard(id: widget.chat, message: widget.message))();
      if (mounted) setState(() => _r = r);
    } on ApiError catch (e) {
      if (mounted) setState(() => _error = e.status == 403 ? 'This business is not taking messages from you.' : e.message);
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _chat(String business, {String? tool}) async {
    final nav = Navigator.of(context);
    nav.pop();
    await chatWithBusiness(nav.context, widget.auth, widget.openBusiness, business: business, from: 'shared', tool: tool);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final r = _r;
    final c = r?.card;
    final live = r?.live;
    Widget line(String words, {bool bold = false, bool quiet = false}) => Padding(
      padding: const EdgeInsets.only(bottom: 4),
      child: Text(words, style: tkText(size: bold ? 17 : 14.5, weight: bold ? 700 : 400, lineHeight: 1.35, color: quiet ? t.muted : t.text)),
    );
    final body = <Widget>[];
    VoidCallback? cta;
    String ctaWords = '';
    if (_error != null) body.add(TkErrorBox(_error!));
    if (r == null && _error == null) body.add(const TkLoading(label: 'Loading…'));
    if (c != null && live != null) {
      final business = c.businessId ?? '';
      switch (c.type) {
        case 'product':
          final p = live.product;
          body.add(line(p?.name ?? c.title, bold: true));
          if ((c.business ?? '').isNotEmpty) body.add(line('From ${c.business}${(c.area ?? '').isEmpty ? '' : ', ${c.area}'}', quiet: true));
          if (p == null) {
            body.add(line('They no longer sell this. What was shared is all there is to see.', quiet: true));
          } else {
            if (p.priceKobo != null) body.add(line('${naira(p.priceKobo!)} now${c.priceKobo != null && c.priceKobo != p.priceKobo ? ' (it was ${naira(c.priceKobo!)} when shared)' : ''}'));
            body.add(line(!p.available ? 'Not available now' : (p.fewLeft != null ? 'Only ${p.fewLeft} left' : 'Available'), quiet: true));
            if ((p.description ?? '').isNotEmpty) body.add(line(p.description!, quiet: true));
          }
          if (business.isNotEmpty) {
            ctaWords = 'Order from their shop';
            cta = () => _chat(business, tool: 'shop');
          }
        case 'business':
          final b = live.business;
          body.add(line(b?.name ?? c.title, bold: true));
          if ((b?.tagline ?? '').isNotEmpty) body.add(line(b!.tagline!));
          final where = [if ((b?.area ?? c.area ?? '').isNotEmpty) (b?.area ?? c.area)!, if (b?.openNow == true) 'Open now' else if (b?.openNow == false) 'Closed now'];
          if (where.isNotEmpty) body.add(line(where.join(' · '), quiet: true));
          if (b == null) body.add(line('This business is not showing itself now.', quiet: true));
          if (business.isNotEmpty && (b == null || b.takesChats)) {
            ctaWords = 'Chat with them';
            cta = () => _chat(business);
          }
        default:
          final o = live.order;
          body.add(line(c.title, bold: true));
          if ((c.business ?? '').isNotEmpty) body.add(line(c.business!, quiet: true));
          if (live.yours && o != null) {
            body.add(line('${yourOrderStatus(o.state)} · ${naira(o.totalKobo)}'));
            if (o.summary.isNotEmpty) body.add(line(o.summary, quiet: true));
            final d = live.delivery;
            if (d != null) {
              body.add(line(d.line, quiet: true));
              body.add(Padding(padding: const EdgeInsets.only(top: 6), child: CardProgress(steps: deliverySteps, step: deliveryStep(d))));
            }
            if (widget.openOrder != null) {
              ctaWords = 'See your order';
              cta = () {
                Navigator.of(context).pop();
                widget.openOrder!(o.businessId, o.reference);
              };
            }
          } else {
            if ((c.state ?? '').isNotEmpty) body.add(line('${yourOrderStatus(c.state!)} when shared'));
            body.add(line('This order is not yours, so only what was shared shows.', quiet: true));
          }
      }
    }
    return TkAppLook(
      child: TkSheet(
        title: c == null ? 'Shared card' : peopleCardKind(c),
        crumb: 'Shared in this chat',
        footer: cta == null ? null : TkCta(label: ctaWords, onTap: cta),
        body: TkFormBody(children: body),
      ),
    );
  }
}

// --- picking people chats ---

/// People chats to send to: up to [max], from the chats you have with
/// people, archived ones too (not requests, not blocked, not a deleted or
/// suspended account); null when put away.
Future<List<String>?> pickPeopleChats(BuildContext context, Auth auth, {required String title, required String what, int max = 5, String? except}) =>
    showTkSheet<List<String>>(context, (_) => _PeoplePicker(auth: auth, title: title, what: what, max: max, except: except));

class _PeoplePicker extends StatefulWidget {
  const _PeoplePicker({required this.auth, required this.title, required this.what, required this.max, this.except});
  final Auth auth;
  final String title;
  final String what;
  final int max;
  final String? except;

  @override
  State<_PeoplePicker> createState() => _PeoplePickerState();
}

class _PeoplePickerState extends State<_PeoplePicker> {
  List<PeopleChat>? _open;
  List<PeopleChat> _away = const [];
  String? _error;
  final _picked = <String>[];

  /// The next page of the chats, then of the archived ones (`before`).
  String? _nextOpen;
  String? _nextAway;

  @override
  void initState() {
    super.initState();
    _load();
  }

  bool _can(PeopleChat c) => !c.request && !c.gone && !c.blocked && c.suspended != true && c.id != widget.except;

  /// The chats, and the archived ones after them.
  List<PeopleChat>? get _chats {
    final open = _open;
    if (open == null) return null;
    final seen = <String>{};
    return [for (final c in [...open, ..._away]) if (_can(c) && seen.add(c.id)) c];
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.listPeopleChats();
      ListPeopleChatsResponse? a;
      try {
        a = await widget.auth.api.listPeopleChats(archived: '1');
      } on Object {
        // The archived ones are left out this time.
      }
      if (!mounted) return;
      setState(() {
        _open = r.chats;
        _nextOpen = r.more ? r.next : null;
        _away = [...?a?.chats.where((c) => c.archived)];
        _nextAway = a != null && a.more ? a.next : null;
      });
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  /// The next page: of the chats while there are more, then of the
  /// archived ones.
  Future<void> _more() async {
    final open = _nextOpen;
    try {
      if (open != null) {
        final r = await widget.auth.api.listPeopleChats(before: open);
        if (mounted) setState(() => (_open = [...?_open, ...r.chats], _nextOpen = r.more ? r.next : null));
      } else if (_nextAway != null) {
        final r = await widget.auth.api.listPeopleChats(archived: '1', before: _nextAway);
        if (mounted) setState(() => (_away = [..._away, ...r.chats.where((c) => c.archived)], _nextAway = r.more ? r.next : null));
      }
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  void _toggle(String id) {
    setState(() {
      if (_picked.remove(id)) return;
      if (_picked.length >= widget.max) {
        _error = widget.max == 1 ? 'Choose one chat.' : 'At most ${widget.max} chats at once.';
        return;
      }
      _error = null;
      _picked.add(id);
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final chats = _chats;
    return TkAppLook(
      child: TkSheet(
        title: widget.title,
        crumb: widget.max > 1 ? 'Up to ${widget.max} chats' : null,
        footer: TkCta(label: _picked.isEmpty ? 'Choose a chat' : 'Send', onTap: _picked.isEmpty ? null : () => Navigator.of(context).pop(List.of(_picked))),
        body: ListView(
          padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
          children: [
            Padding(padding: const EdgeInsets.fromLTRB(4, 0, 4, 10), child: Text('“${widget.what}”', maxLines: 2, overflow: TextOverflow.ellipsis, style: tkHint(t))),
            if (_error != null) TkErrorBox(_error!),
            if (chats == null && _error == null) const TkLoading(label: 'Loading…'),
            if (chats != null && chats.isEmpty) const TkPanel(children: [TkEmpty(icon: 'chat', title: 'No chats with people yet', line: 'Start one from New chat on your Chats.')]),
            if (chats != null && chats.isNotEmpty)
              TkPanel(
                children: [
                  for (final c in chats)
                    Semantics(
                      checked: _picked.contains(c.id),
                      child: TkChoiceRow(
                        radio: widget.max == 1,
                        control: widget.max == 1 ? TkRadio(checked: _picked.contains(c.id)) : TkCheckbox(checked: _picked.contains(c.id)),
                        label: Row(
                          children: [
                            PersonFace(name: c.name, transport: widget.auth.transport, photo: c.person?.photo, size: 36),
                            const SizedBox(width: 10),
                            Expanded(child: Text(c.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 15, color: t.text))),
                          ],
                        ),
                        onTap: () => _toggle(c.id),
                      ),
                    ),
                ],
              ),
            if (chats != null && (_nextOpen != null || _nextAway != null)) TkLoadMore(label: 'Show more chats', onMore: _more),
          ],
        ),
      ),
    );
  }
}

/// Client ids of shares that may have reached a chat without an answer
/// (offline, cut off, the server failing), by chat and card: the same
/// card shared to the same chat again goes with the same id, so the server
/// keeps one.
final _unsureShares = <String, String>{};
var _shareN = 0;

/// Shares [card] (a business, one of its products, one of your orders)
/// into chats with people the person picks (§10.1); says how it went.
Future<void> shareWithPeople(BuildContext context, Auth auth, SendPeopleMessageRequestCard card, {required String what}) async {
  final to = await pickPeopleChats(context, auth, title: 'Share with', what: what);
  if (to == null || to.isEmpty || !context.mounted) return;
  var sent = 0;
  String? problem;
  final shared = jsonEncode(card.toJson());
  for (final id in to) {
    final key = '$id $shared';
    final client = _unsureShares.remove(key) ?? newClientId(DateTime.now(), ++_shareN);
    try {
      await auth.api.sendPeopleMessage(id: id, body: SendPeopleMessageRequest(card: card, clientId: client));
      sent++;
    } on Object catch (e) {
      if (e is! ApiError || e.status >= 500) _unsureShares[key] = client;
      problem ??= sayFailure(e);
    }
  }
  if (!context.mounted) return;
  if (problem != null) {
    showTkToast(context, sent == 0 ? problem : 'Shared with $sent. $problem', error: true);
  } else {
    showTkToast(context, sent == 1 ? 'Shared' : 'Shared with $sent chats');
  }
}

// --- what to share, from a people chat ---

/// What the + of a people chat offers.
enum PeopleShare { photo, file, place, business, product, order, delivery, split, together }

/// The + by the box of a people chat: a photo or a file, a card of a
/// business you chat with, one of its products, one of your orders or
/// its delivery, or your share of something paid.
///
/// [together]: "Order together" is offered (a group one may post in, or
/// a people chat no longer a request: 5b C).
Future<PeopleShare?> showPeopleShareMenu(BuildContext context, {bool together = false}) => showTkSheet<PeopleShare>(context, (_) => _ShareMenu(together: together));

class _ShareMenu extends StatelessWidget {
  const _ShareMenu({this.together = false});
  final bool together;

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    Widget row(PeopleShare k, String icon, Color colour, String name, String sub) =>
        TkListRow(title: name, sub: sub, icon: icon, square: colour, onTap: () => Navigator.of(context).pop(k));
    return TkAppLook(
      child: TkSheet(
        title: 'Send',
        body: ListView(
          padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
          children: [
            TkSection(
              inset: 56,
              children: [
                row(PeopleShare.photo, 'b-camera', look.teal, 'A photo', 'Up to 10 MB'),
                row(PeopleShare.file, 'b-note', look.blue, 'A PDF', 'Up to 25 MB'),
                row(PeopleShare.place, 'b-pin', look.red, 'A place', 'Where you are now, sent once'),
              ],
            ),
            const SizedBox(height: 14),
            TkSection(
              inset: 56,
              children: [
                row(PeopleShare.business, 'b-store', look.teal, 'A business', 'One you chat with'),
                row(PeopleShare.product, 'b-bag', look.orange, 'A product', 'From the shop of a business you chat with'),
                row(PeopleShare.order, 'b-box', look.blue, 'One of your orders', 'Its card: what, and its state'),
                row(PeopleShare.delivery, 'b-bike', look.violet, 'A delivery', 'How far the rider has got with your order'),
              ],
            ),
            const SizedBox(height: 14),
            TkSection(
              inset: 56,
              children: [
                if (together) row(PeopleShare.together, 'b-bag', look.orange, 'Order together', 'A shared basket from one business: each adds their own'),
                row(PeopleShare.split, 'b-money', look.lime, 'Your share', 'Ask them for their share of something you paid'),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

/// A card to share of [kind]: chosen from the person's business chats,
/// a business's shop, or their orders; null when put away.
Future<SendPeopleMessageRequestCard?> pickShareCard(BuildContext context, Auth auth, PeopleShare kind) =>
    showTkSheet<SendPeopleMessageRequestCard>(context, (_) => _CardPicker(auth: auth, kind: kind));

class _CardPicker extends StatefulWidget {
  const _CardPicker({required this.auth, required this.kind});
  final Auth auth;
  final PeopleShare kind;

  @override
  State<_CardPicker> createState() => _CardPickerState();
}

class _CardPickerState extends State<_CardPicker> {
  List<PersonChat>? _chats;
  List<PersonOrder>? _orders;

  /// A product's business, chosen: its shop's items.
  PersonChat? _shopOf;
  List<ShopItem>? _items;
  String? _error;

  TaskiemApi get _api => widget.auth.api;
  bool get _byOrder => widget.kind == PeopleShare.order || widget.kind == PeopleShare.delivery;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      if (_byOrder) {
        final r = await _api.myOrders();
        if (mounted) setState(() => _orders = r.orders);
      } else {
        final r = await _api.listMyChats();
        if (mounted) setState(() => _chats = r.chats.where((c) => !c.blockedYou).toList());
      }
    } on ApiError catch (e) {
      if (mounted) setState(() => e.status == 404 ? (_byOrder ? _orders = const [] : _chats = const []) : _error = e.message);
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _shop(PersonChat c) async {
    setState(() => (_shopOf = c, _items = null, _error = null));
    try {
      final r = await _api.myChatShop(id: c.id);
      if (mounted) setState(() => _items = r.items.where((i) => i.available).toList());
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  void _pick(SendPeopleMessageRequestCard c) => Navigator.of(context).pop(c);

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final rows = <Widget>[];
    final shopOf = _shopOf;
    if (_byOrder) {
      final orders = _orders;
      if (orders != null && orders.isEmpty) rows.add(const TkPanel(children: [TkEmpty(icon: 'b-box', title: 'No orders yet', line: 'Orders you place with businesses show here.')]));
      if (orders != null && orders.isNotEmpty) {
        rows.add(TkSection(
          inset: 56,
          children: [
            for (final o in orders)
              TkListRow(
                title: '${o.business} · ${o.reference}',
                sub: o.summary.isEmpty ? yourOrderStatus(o.state) : o.summary,
                icon: widget.kind == PeopleShare.delivery ? 'b-bike' : 'b-box',
                square: widget.kind == PeopleShare.delivery ? look.violet : look.blue,
                amount: naira(o.totalKobo),
                onTap: () => _pick(SendPeopleMessageRequestCard(type: widget.kind == PeopleShare.delivery ? 'delivery' : 'order', businessId: o.businessId, reference: o.reference)),
              ),
          ],
        ));
      }
    } else if (shopOf != null) {
      final items = _items;
      if (items != null && items.isEmpty) rows.add(const TkPanel(children: [TkEmpty(icon: 'b-bag', title: 'Nothing to share', line: 'Their shop has nothing available now.')]));
      if (items != null && items.isNotEmpty) {
        rows.add(TkSection(
          inset: 56,
          children: [
            for (final i in items)
              TkListRow(
                title: i.name,
                sub: i.category ?? '',
                icon: 'b-bag',
                square: look.orange,
                amount: i.priceKobo == null ? null : naira(i.priceKobo!),
                onTap: () => _pick(SendPeopleMessageRequestCard(type: 'product', businessId: shopOf.business, itemId: i.id)),
              ),
          ],
        ));
      }
    } else {
      final chats = _chats;
      if (chats != null && chats.isEmpty) rows.add(const TkPanel(children: [TkEmpty(icon: 'b-store', title: 'No businesses yet', line: 'Businesses you chat with show here.')]));
      if (chats != null && chats.isNotEmpty) {
        rows.add(TkSection(
          inset: 56,
          children: [
            for (final c in chats)
              TkListRow(
                title: c.name,
                sub: widget.kind == PeopleShare.product ? 'See their shop' : 'Share their card',
                icon: 'b-store',
                square: look.teal,
                onTap: () => widget.kind == PeopleShare.product ? _shop(c) : _pick(SendPeopleMessageRequestCard(type: 'business', businessId: c.business)),
              ),
          ],
        ));
      }
    }
    final loading = _error == null && ((_byOrder && _orders == null) || (!_byOrder && shopOf == null && _chats == null) || (shopOf != null && _items == null));
    return TkAppLook(
      child: TkSheet(
        title: switch (widget.kind) {
          PeopleShare.product => shopOf == null ? 'A product' : shopOf.name,
          PeopleShare.order => 'One of your orders',
          PeopleShare.delivery => 'A delivery',
          _ => 'A business',
        },
        crumb: 'Share',
        onBack: shopOf == null ? null : () => setState(() => (_shopOf = null, _items = null)),
        body: ListView(
          padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
          children: [
            if (_error != null) TkErrorBox(_error!),
            if (loading) const TkLoading(label: 'Loading…'),
            ...rows,
          ],
        ),
      ),
    );
  }
}

// --- your share ---

/// "Your share" (§11 A): how much, what for, and the bank account to be
/// paid to (your saved one, or one typed here). Null when put away.
Future<SendPeopleMessageRequestCard?> showSplitForm(BuildContext context, Auth auth) =>
    showTkSheet<SendPeopleMessageRequestCard>(context, (_) => _SplitForm(auth: auth));

class _SplitForm extends StatefulWidget {
  const _SplitForm({required this.auth});
  final Auth auth;

  @override
  State<_SplitForm> createState() => _SplitFormState();
}

class _SplitFormState extends State<_SplitForm> {
  final _amount = TextEditingController();
  final _what = TextEditingController();
  final _bank = TextEditingController();
  final _number = TextEditingController();
  final _name = TextEditingController();
  PayoutAccount? _saved;
  bool _loaded = false;

  /// Typing another account than the saved one.
  bool _other = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.getPayoutAccount();
      final a = r.account;
      if (mounted) setState(() => _saved = a is Map ? PayoutAccount.fromJson(a.cast<String, dynamic>()) : null);
    } on Object {
      // None saved: typed here.
    }
    if (mounted) setState(() => _loaded = true);
  }

  @override
  void dispose() {
    for (final c in [_amount, _what, _bank, _number, _name]) {
      c.dispose();
    }
    super.dispose();
  }

  void _send() {
    final kobo = nairaKobo(_amount.text);
    final what = _what.text.trim();
    if (kobo == null || kobo < 100) return setState(() => _error = 'Write how much their share is, at least ₦1.');
    if (what.isEmpty) return setState(() => _error = 'Write what it is for.');
    PayoutAccount? to;
    if (_saved == null || _other) {
      final number = _number.text.replaceAll(RegExp(r'\s'), '');
      if (_bank.text.trim().isEmpty || !RegExp(r'^\d{10}$').hasMatch(number) || _name.text.trim().isEmpty) {
        return setState(() => _error = 'Write the bank, the 10-digit account number and the name on the account.');
      }
      to = PayoutAccount(bank: _bank.text.trim(), accountNumber: number, accountName: _name.text.trim());
    }
    Navigator.of(context).pop(SendPeopleMessageRequestCard(type: 'split', amountKobo: kobo, what: what, payTo: to));
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final saved = _saved;
    return TkAppLook(
      child: TkSheet(
        title: 'Your share',
        crumb: 'Ask for their part of something you paid',
        footer: TkCta(label: 'Send', onTap: _loaded ? _send : null),
        body: TkFormBody(
          children: [
            TkMoneyField(label: 'Their share', controller: _amount, hint: 'In naira'),
            TkKitField(label: 'What it is for', child: TkTextBox(controller: _what, hint: 'The cake for Bisi', max: 120)),
            if (saved != null && !_other) ...[
              TkKitField(label: 'Paid to', hint: 'Your saved account', field: false, child: Text(accountLine(saved), style: tkText(size: 15, weight: 600, color: t.text))),
              TkLinkButton(label: 'Use another account', onPressed: () => setState(() => _other = true)),
            ] else ...[
              TkKitField(label: 'Bank', child: TkTextBox(controller: _bank, hint: 'GTBank')),
              TkKitField(label: 'Account number', child: TkTextBox(controller: _number, digits: 10)),
              TkKitField(label: 'Name on the account', child: TkTextBox(controller: _name)),
            ],
            Text('They pay you by their bank app. Taskiem moves no money and does not check the account.', style: tkHint(t)),
            if (_error != null) TkErrorBox(_error!),
          ],
        ),
      ),
    );
  }
}

// --- the bank account to be paid to ---

/// Settings > Your bank account for shares (`/v1/me/payout-account`):
/// the account a share asks friends to pay to. Kept sealed; never checked
/// by Taskiem, which moves no money.
class PayoutAccountPage extends StatefulWidget {
  const PayoutAccountPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<PayoutAccountPage> createState() => _PayoutAccountPageState();
}

class _PayoutAccountPageState extends State<PayoutAccountPage> {
  final _bank = TextEditingController();
  final _number = TextEditingController();
  final _name = TextEditingController();
  PayoutAccountAnswer? _a;
  String? _error;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _bank.dispose();
    _number.dispose();
    _name.dispose();
    super.dispose();
  }

  void _show(PayoutAccountAnswer a) {
    final acc = a.account;
    if (acc is Map) {
      final p = PayoutAccount.fromJson(acc.cast<String, dynamic>());
      _bank.text = p.bank;
      _number.text = p.accountNumber;
      _name.text = p.accountName;
    } else {
      _bank.clear();
      _number.clear();
      _name.clear();
    }
    _a = a;
  }

  Future<void> _load() async {
    try {
      final a = await widget.auth.api.getPayoutAccount();
      if (mounted) setState(() => _show(a));
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _save() async {
    final number = _number.text.replaceAll(RegExp(r'\s'), '');
    if (_bank.text.trim().isEmpty || !RegExp(r'^\d{10}$').hasMatch(number) || _name.text.trim().isEmpty) {
      return setState(() => _error = 'Write the bank, the 10-digit account number and the name on the account.');
    }
    setState(() => (_busy = true, _error = null));
    try {
      final a = await widget.auth.api.putPayoutAccount(body: PayoutAccount(bank: _bank.text.trim(), accountNumber: number, accountName: _name.text.trim()));
      if (!mounted) return;
      setState(() => _show(a));
      showTkToast(context, 'Saved');
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<void> _remove() async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.api.deletePayoutAccount();
      if (!mounted) return;
      setState(() => _show(const PayoutAccountAnswer(account: null, checked: false, words: '')));
      showTkToast(context, 'Removed');
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final a = _a;
    return TkScreen(
      title: 'Your bank account',
      children: [
        if (a == null && _error == null) const TkLoading(label: 'Loading…'),
        TkPanel(
          padding: const EdgeInsets.all(16),
          children: [
            Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text(
                  (a?.words ?? '').isNotEmpty ? a!.words : 'Friends pay your share to this account, by their own bank app. Taskiem moves no money and does not check it.',
                  style: tkHint(t),
                ),
                const SizedBox(height: 12),
                TkKitField(label: 'Bank', child: TkTextBox(controller: _bank, hint: 'GTBank')),
                const SizedBox(height: 12),
                TkKitField(label: 'Account number', child: TkTextBox(controller: _number, digits: 10)),
                const SizedBox(height: 12),
                TkKitField(label: 'Name on the account', child: TkTextBox(controller: _name)),
                const SizedBox(height: 14),
                TkButton(label: _busy ? 'Saving…' : 'Save', primary: true, minHeight: 44, enabled: !_busy, onPressed: _save),
                if (a?.account is Map) ...[const SizedBox(height: 8), TkButton(label: 'Remove it', minHeight: 44, enabled: !_busy, onPressed: _remove)],
              ],
            ),
          ],
        ),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}

/// A share card's request in words, for the picker's line.
String shareWhat(SendPeopleMessageRequestCard c) => switch (c.type) {
  'split' => 'Your share: ${naira(c.amountKobo ?? 0)}',
  'business' => 'A business',
  'product' => 'A product',
  'delivery' => 'A delivery',
  _ => 'An order${_str(c.reference).isEmpty ? '' : ' ${c.reference}'}',
};
