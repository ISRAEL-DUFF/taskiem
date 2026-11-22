// Collecting people to pay from several messages (paying people, P3;
// docs/spec/chat-pay-people.md "Pay several people → Filling the list →
// From messages"): "Add to the list" beside "Pay this account" (under a
// message, or on a chat photo's menu) keeps that message's reading on the
// person's own list, one across every chat, kept on the server
// (/v1/pay-collection). While it has rows, "3 to pay · ₦95,000" stays at
// the foot of the chat, above the box, with Pay these (and Clear behind a
// menu); Pay these opens Pay several people on the list, each row with
// the message it came from, and Check makes the list a batch (POST
// /v1/pay-collection/batch) checked and paid as any other. A batch made
// from it waiting to be sent: "Waiting to be sent", opening that batch.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../kit/icon.dart' show TkIcon;
import '../kit/kit.dart' show TkAppLook, TkPress;
import '../kit/modal.dart' show showTkToast;
import '../kit/sheet.dart' show showTkSheet;
import '../kit/tap_area.dart' show TapArea;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'cards.dart' show CardButton;
import 'groups.dart' show askSure, pickAction;
import 'my_chat.dart' show sayFailure;
import 'pay_chip.dart' show PayForms, askToSave, mayPayFromMessages;
import 'pay_many.dart' show BatchSheet, PayManySheet, PayRow;
import 'pay_sheet.dart' show pickPayBank;
import 'words.dart' show naira;

/// The message a row comes from: its chat's kind (taskiem, link,
/// customer, person, group), the chat's id and the message's; [from], who
/// sent it, when the chat knows ('' when not).
typedef MessageFrom = ({String kind, String chat, String message, String from});

/// The person's list of people to pay, as the server keeps it: one per
/// person across every chat, read when a chat opens and after each change.
class PayList extends ChangeNotifier {
  PayList._(this.auth);
  final Auth auth;

  static final _of = Expando<PayList>();

  static PayList of(Auth auth) => _of[auth] ??= PayList._(auth);

  /// The list as last read; null before.
  PayCollection? list;

  /// Who sent each message added on this phone, by its id, for a list
  /// read before the server's answer names them (the server keeps it too:
  /// the source's `from`).
  static final Map<String, String> senders = {};

  Future<void>? _reading;

  int get count => list?.count ?? 0;

  /// A batch made from the list that waits to be sent, if any.
  String? get inBatch => switch (list?.inBatch) {
    final String id when id.isNotEmpty => id,
    _ => null,
  };

  /// Read again (once at a time); a failure leaves it as it was.
  Future<void> refresh() => _reading ??= () async {
    try {
      took(await auth.api.getPayCollection());
    } on Object {
      // The foot stays as it was.
    } finally {
      _reading = null;
    }
  }();

  void took(PayCollection c) {
    list = c;
    notifyListeners();
  }
}

/// "3 to pay".
String toPayWords(int n) => '$n to pay';

/// Where a row came from, in words: "from Chidi Okeke's message", or the
/// kind of chat when who sent it is not known.
String sourceWords(PayCollectionRowsItem x) {
  final s = x.source;
  if (s is! Map) return '';
  final kept = '${s['from'] ?? ''}'.trim();
  final from = kept.isNotEmpty ? kept : PayList.senders['${s['message']}'] ?? '';
  if (from.isNotEmpty) return 'from $from’s message';
  return switch ('${s['kind']}') {
    'customer' => 'from a customer’s message',
    'group' => 'from a message in a group',
    'person' => 'from a message in your chats',
    'taskiem' => 'from Taskiem’s chat',
    'photo' => 'from a photo',
    _ => 'from a message',
  };
}

/// A row of the list as Pay several people shows it.
PayRow collectedRow(PayCollectionRowsItem x) {
  final payee = x.payee.trim();
  final who = x.name.isNotEmpty ? x.name : (payee.isNotEmpty ? '@$payee' : (x.title.isNotEmpty ? x.title : 'Account ••${x.last4}'));
  final account = payee.isNotEmpty && who != '@$payee' ? '@$payee' : [x.bankName, if (x.last4.isNotEmpty) '••${x.last4}'].where((w) => w.isNotEmpty).join(' ');
  final kobo = x.amountKobo is num ? (x.amountKobo as num).toInt() : null;
  return PayRow(
    who: who,
    sub: [sourceWords(x), account].where((w) => w.isNotEmpty).join(' · '),
    ask: const {},
    kobo: kobo,
    purpose: x.purpose,
    collected: x.id,
  );
}

/// What Taskiem read of a message ([r]) added to the list, as the chip
/// pays it: the business's saved account by its @name for staff (or when
/// [byName]), else the number and bank. Said in a toast: "Added: 3 to pay",
/// or that it is there already.
Future<void> addReadingToList(BuildContext context, Auth auth, PayForms forms, ReadPaymentResponse r, MessageFrom from, {bool byName = false}) async {
  final payee = (r.payee ?? '').trim();
  final bySaved = payee.isNotEmpty && (byName || forms.account == null);
  if (!bySaved && forms.account == null) {
    showTkToast(context, askToSave);
    return;
  }
  // An account number read with no bank: which bank, asked first (the
  // server keeps no account without one).
  var bank = bySaved ? '' : (r.bankName ?? '').trim();
  if (!bySaved && bank.isEmpty) {
    final List<ListPayBanksResponseBanksItem> banks;
    try {
      banks = (await auth.api.listPayBanks(account: r.accountNumber)).banks;
    } on Object catch (e) {
      if (context.mounted) showTkToast(context, sayFailure(e), error: true);
      return;
    }
    if (!context.mounted) return;
    final v = await pickPayBank(context, banks);
    if (v == null || v.trim().isEmpty || !context.mounted) return;
    bank = v.trim();
  }
  final kobo = r.amountKobo ?? 0;
  final list = PayList.of(auth);
  if (from.from.isNotEmpty) PayList.senders[from.message] = from.from;
  try {
    final c = await auth.api.addPayCollection(
      body: AddPayCollectionRequest(
        payee: bySaved ? payee : null,
        accountNumber: bySaved ? null : r.accountNumber,
        bankName: bySaved ? null : bank,
        name: (r.name ?? '').trim().isEmpty ? null : r.name!.trim(),
        amountKobo: kobo >= 100 ? kobo : null,
        source: PayCollectionSource(kind: from.kind, chat: from.chat, message: from.message, from: from.from.isEmpty ? null : from.from),
      ),
    );
    list.took(c);
    if (context.mounted) showTkToast(context, 'Added: ${toPayWords(c.count)}');
  } on ApiError catch (e) {
    if (e.body['code'] == 'already') {
      await list.refresh();
      if (context.mounted) showTkToast(context, 'Already on your list: ${toPayWords(list.count)}');
      return;
    }
    if (context.mounted) showTkToast(context, e.message, error: true);
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, sayFailure(e), error: true);
  }
}

/// Pay these: Pay several people on the list (read first), in the app's
/// look; the list read again once it closes.
Future<void> openPayList(BuildContext context, Auth auth, {bool follow = true}) async {
  final list = PayList.of(auth);
  final PayCollection c;
  try {
    c = await auth.api.getPayCollection();
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, sayFailure(e), error: true);
    return;
  }
  list.took(c);
  if (!context.mounted) return;
  if (list.inBatch != null) return openListBatch(context, auth, follow: follow);
  if (c.rows.isEmpty) {
    showTkToast(context, 'Your list is empty: add someone from a message.');
    return;
  }
  await showTkSheet<PaymentBatch>(
    context,
    (_) => TkAppLook(
      child: PayManySheet(auth: auth, kind: 'messages', title: 'From messages', collected: c, follow: follow),
    ),
  );
  await list.refresh();
}

/// The batch made from the list, waiting to be sent: opened to send or
/// cancel it.
Future<void> openListBatch(BuildContext context, Auth auth, {bool follow = true}) async {
  final list = PayList.of(auth);
  final id = list.inBatch;
  if (id == null) return;
  final PaymentBatch b;
  try {
    b = (await auth.api.getPaymentBatch(id: id)).batch;
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, sayFailure(e), error: true);
    return;
  }
  if (!context.mounted) return;
  await showTkSheet<PaymentBatch>(
    context,
    (_) => TkAppLook(
      child: BatchSheet(auth: auth, batch: b, follow: follow),
    ),
  );
  await list.refresh();
}

/// The list cleared, asked first.
Future<void> _clear(BuildContext context, Auth auth) async {
  final list = PayList.of(auth);
  final n = list.count;
  final yes = await askSure(context, title: 'Clear your list?', body: 'The $n to pay are taken off it. Nothing is paid.', ok: 'Clear the list', danger: true);
  if (!yes || !context.mounted) return;
  try {
    list.took(await auth.api.clearPayCollection());
    if (context.mounted) showTkToast(context, 'Cleared: nothing was paid');
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, sayFailure(e), error: true);
  }
}

/// The foot of a chat while the list has rows: "3 to pay · ₦95,000" (and
/// "1 needs an amount") with Pay these, and Clear behind its menu. Read
/// when the chat opens; nothing for those who may not pay from messages.
class PayListBar extends StatefulWidget {
  const PayListBar({super.key, required this.auth, this.follow = true});
  final Auth auth;

  /// Read the batch again while rows are sent (not in a test's pictures).
  final bool follow;

  @override
  State<PayListBar> createState() => _PayListBarState();
}

class _PayListBarState extends State<PayListBar> {
  late final PayList _list = PayList.of(widget.auth);
  bool _opening = false;

  bool get _may => mayPayFromMessages(widget.auth);

  @override
  void initState() {
    super.initState();
    if (_may) unawaited(_list.refresh());
  }

  Future<void> _run(Future<void> Function() f) async {
    if (_opening) return;
    _opening = true;
    try {
      await f();
    } finally {
      _opening = false;
    }
  }

  Future<void> _more() async {
    final k = await pickAction(context, title: toPayWords(_list.count), actions: [('clear', 'Clear the list', 'b-x', true)]);
    if (k == 'clear' && mounted) await _clear(context, widget.auth);
  }

  @override
  Widget build(BuildContext context) {
    if (!_may) return const SizedBox.shrink();
    return ListenableBuilder(
      listenable: _list,
      builder: (context, _) {
        final c = _list.list;
        final waiting = _list.inBatch != null;
        if (c == null || (c.count == 0 && !waiting)) return const SizedBox.shrink();
        final t = TkTheme.tokens(context);
        final look = TkLook.of(context);
        final title = waiting ? 'Waiting to be sent' : [toPayWords(c.count), if (c.totalKobo > 0) naira(c.totalKobo)].join(' · ');
        final sub = waiting
            ? (c.count > 0 ? '${toPayWords(c.count)} more after it' : 'Your list, as a batch')
            : (c.noAmount > 0 ? '${c.noAmount} ${c.noAmount == 1 ? 'needs' : 'need'} an amount' : null);
        return Semantics(
          container: true,
          liveRegion: true,
          child: Container(
            margin: const EdgeInsets.fromLTRB(10, 6, 10, 0),
            padding: const EdgeInsets.fromLTRB(8, 4, 4, 4),
            decoration: BoxDecoration(
              color: look.card,
              borderRadius: BorderRadius.circular(18),
              boxShadow: const [BoxShadow(color: Color(0x140E1A14), offset: Offset(0, 1), blurRadius: 2)],
            ),
            child: Row(
              children: [
                ExcludeSemantics(
                  child: Container(
                    width: 30,
                    height: 30,
                    alignment: Alignment.center,
                    decoration: BoxDecoration(color: waiting ? look.orange : look.teal, shape: BoxShape.circle),
                    child: TkIcon(waiting ? 'b-clock' : 'b-people', size: 16, stroke: 2.2, color: const Color(0xFFFFFFFF)),
                  ),
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        title,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: tkText(size: 14, weight: 650, lineHeight: 1.3, color: t.text),
                      ),
                      if (sub != null)
                        Text(
                          sub,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: tkText(
                            size: 12.5,
                            weight: c.noAmount > 0 && !waiting ? 600 : 400,
                            lineHeight: 1.3,
                            color: c.noAmount > 0 && !waiting ? look.orangeInk : t.muted,
                          ),
                        ),
                    ],
                  ),
                ),
                const SizedBox(width: 8),
                CardButton(
                  label: waiting ? 'Open' : 'Pay these',
                  primary: true,
                  semantics: waiting ? 'Open the batch waiting to be sent' : 'Pay these ${toPayWords(c.count)}',
                  onTap: () => unawaited(
                    _run(() => waiting ? openListBatch(context, widget.auth, follow: widget.follow) : openPayList(context, widget.auth, follow: widget.follow)),
                  ),
                ),
                if (!waiting)
                  TapArea(
                    child: Semantics(
                      button: true,
                      label: 'More: clear the list',
                      excludeSemantics: true,
                      onTap: () => unawaited(_run(_more)),
                      child: TkPress(
                        onTap: () => unawaited(_run(_more)),
                        child: SizedBox(
                          width: 48,
                          height: 48,
                          child: Center(child: TkIcon('b-more', size: 20, stroke: 2.2, color: t.muted)),
                        ),
                      ),
                    ),
                  ),
              ],
            ),
          ),
        );
      },
    );
  }
}
