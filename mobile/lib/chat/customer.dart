// A customer's conversation (docs/spec/chat-five-more-flows.md, phase 1;
// web/src/pages/CustomerChat.tsx, lib/customerchat.ts): a customer who
// wrote to the business's own WhatsApp number is a thread in the chat;
// staff who handle orders answer there, and the answer goes from the
// business's number. Within 24 hours of their last message a reply goes as
// words; after, as a WhatsApp message they answer to carry on (on one
// line, up to 900 characters). A customer who sent STOP gets nothing.

import 'dart:async';

import 'package:flutter/material.dart' show InputDecoration, Material, MaterialType, TextField;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/modal.dart';
import '../theme/theme.dart';
import 'chats.dart';
import 'thread.dart';
import 'words.dart';

// --- words (lib/customerchat.ts) ---

/// customerchat.ts phoneWords: "+234 803 111 2222".
String phoneWords(String phone) {
  final m = RegExp(r'^\+234(\d{3})(\d{3})(\d{4})$').firstMatch(phone);
  return m == null ? phone : '+234 ${m[1]} ${m[2]} ${m[3]}';
}

/// customerchat.ts customerTitle: their name in the book, or their number.
String customerTitle(CustomerChat c) => c.name == c.phone ? phoneWords(c.phone) : c.name;

/// customerchat.ts customerLetter: initials of a known customer, else "Customer".
String customerLetter(CustomerChat c) => c.name == c.phone || RegExp(r'^\+?\d').hasMatch(c.name) ? 'Customer' : c.name;

/// customerchat.ts replyNote: what the box says about how a reply goes.
({bool stop, String text})? replyNote(CustomerChat c) {
  if (c.stopped) return (stop: true, text: 'They asked not to get messages (they sent STOP). Nothing can be sent until they send START.');
  if (c.phone.isEmpty) return (stop: true, text: 'This customer’s details were erased.');
  if (!c.windowOpen) {
    return (stop: false, text: 'More than a day since they last wrote: your reply goes as a WhatsApp message they answer to carry on.');
  }
  return null;
}

/// customerchat.ts owesWords: "Owes ₦12,500 on 2 invoices".
String owesWords(int? kobo, int? invoices) {
  if (kobo == null || kobo <= 0) return '';
  final n = invoices ?? 0;
  return 'Owes ${naira(kobo)}${n > 0 ? ' on $n invoice${n == 1 ? '' : 's'}' : ''}';
}

/// customerchat.ts LATE_REPLY_MAX, lateLength, lateTooLong.
const lateReplyMax = 900;
int lateLength(String text) => text.trim().split(RegExp(r'\s+')).join(' ').runes.length;
bool lateTooLong(String text, bool windowOpen) => !windowOpen && lateLength(text) > lateReplyMax;

/// customerchat.ts mergeCustomer: by id, oldest first.
List<CustomerLine> mergeCustomer(List<CustomerLine> cur, List<CustomerLine> more) {
  final by = {for (final m in cur) m.id: m};
  for (final m in more) {
    by[m.id] = m;
  }
  return by.values.toList()
    ..sort((a, b) {
      final t = a.m.createdAt.compareTo(b.m.createdAt);
      return t != 0 ? t : a.id.compareTo(b.id);
    });
}

/// customerchat.ts latestPage: the latest page in place of what it covers;
/// older ones kept when there are more, and sends under way.
List<CustomerLine> latestPage(List<CustomerLine> cur, List<CustomerLine> page, bool more) {
  final from = page.isEmpty ? null : page.first.m.createdAt;
  final kept = cur.where((m) => m.local || (more && from != null && m.m.createdAt.isBefore(from))).toList();
  return mergeCustomer(kept, page);
}

/// A message of the thread; a reply of ours on its way is [local].
class CustomerLine {
  CustomerLine(this.m, {this.local = false});
  final CustomerMessage m;
  final bool local;
  String get id => m.id;
}

/// CustomerChat.tsx POLL_MS.
const _poll = Duration(seconds: 4);

/// What a customer's conversation shows.
class CustomerData extends ChangeNotifier {
  CustomerData(this.chats, this.id, {this.follow = true});
  final ChatsData chats;
  final String id;
  final bool follow;

  TaskiemApi get api => chats.api;
  CustomerChat? chat;
  List<CustomerLine>? messages;
  bool busy = false;
  String? error;
  Timer? _timer;
  bool _disposed = false;
  int _localN = 0;

  /// The words given back to the box when a reply could not go.
  final _back = StreamController<String>.broadcast();
  Stream<String> get givenBack => _back.stream;

  Future<void> open() async {
    await Future.wait([_chat(), _pull(first: true)]);
    // Closed before the first page came: nothing to follow.
    if (follow && !_disposed) {
      _timer = Timer.periodic(_poll, (_) {
        _pull(first: false);
        _chat();
      });
    }
  }

  Future<void> _chat() async {
    try {
      chat = await api.getCustomerChat(id: id);
      _changed();
    } on Object catch (e) {
      _fail(e);
    }
  }

  /// There are older messages than those shown (CustomerChat.tsx).
  bool hasOlder = false;
  bool _loadingOlder = false;

  /// The page before the oldest shown.
  Future<void> older() async {
    final oldest = messages?.where((m) => !m.local).firstOrNull;
    if (oldest == null || _loadingOlder) return;
    _loadingOlder = true;
    try {
      final out = await api.listCustomerMessages(id: id, before: oldest.m.id);
      if (_disposed) return;
      messages = mergeCustomer(messages ?? const [], [for (final m in out.messages) CustomerLine(m)]);
      hasOlder = out.more;
      _changed();
    } on Object catch (e) {
      _fail(e);
    } finally {
      _loadingOlder = false;
    }
  }

  /// The latest page; the customer's messages read as they are shown.
  Future<void> _pull({required bool first}) async {
    try {
      final out = await api.listCustomerMessages(id: id);
      if (_disposed) return;
      final page = [for (final m in out.messages) CustomerLine(m)];
      if (first) hasOlder = out.more;
      messages = first ? page : latestPage(messages ?? const [], page, out.more);
      _changed();
      if (out.messages.any((m) => !m.mine && m.readAt == null)) {
        await api.readCustomerChat(id: id);
        unawaited(chats.load());
      }
    } on Object catch (e) {
      _fail(e);
    }
  }

  void _fail(Object e) {
    if (_disposed) return;
    error = e is ApiError ? e.message : (e is Offline ? 'You’re offline. The conversation shows when you’re back online.' : null);
    _changed();
  }

  /// A reply: shown at once with a clock, then the server's copy; not sent,
  /// the words come back to the box.
  Future<void> send(String text) async {
    final words = text.trim();
    final c = chat;
    if (words.isEmpty || c == null || c.stopped || c.phone.isEmpty || busy) return;
    final client = '${DateTime.now().millisecondsSinceEpoch}-${++_localN}';
    final mine = CustomerLine(
      CustomerMessage(id: 'local-$client', mine: true, kind: 'text', body: words, createdAt: chats.now()),
      local: true,
    );
    messages = mergeCustomer(messages ?? const [], [mine]);
    busy = true;
    error = null;
    _changed();
    try {
      final out = await api.replyCustomer(id: id, body: ReplyCustomerRequest(text: words, clientId: client));
      messages = mergeCustomer((messages ?? const []).where((m) => m != mine).toList(), [CustomerLine(out)]);
      unawaited(chats.load());
    } on Object catch (e) {
      messages = messages?.where((m) => m != mine).toList();
      // Given back only while the screen is there to take it.
      if (!_back.isClosed) _back.add(words);
      _fail(e);
      unawaited(_chat());
    } finally {
      busy = false;
      _changed();
    }
  }

  /// Archive, or bring back to the chats; false if it could not be.
  Future<bool> setArchived(bool archived) async {
    try {
      await api.putCustomerChat(id: id, body: PutCustomerChatRequest(archived: archived));
      unawaited(chats.load());
      await _chat();
      return true;
    } on Object catch (e) {
      _fail(e);
      return false;
    }
  }

  void _changed() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _timer?.cancel();
    _back.close();
    super.dispose();
  }
}

/// CustomerChat.tsx CustomerPane.
class CustomerScreen extends StatefulWidget {
  const CustomerScreen({super.key, required this.data, this.onBack});
  final CustomerData data;
  final VoidCallback? onBack;

  @override
  State<CustomerScreen> createState() => _CustomerScreenState();
}

class _CustomerScreenState extends State<CustomerScreen> {
  final _text = TextEditingController();
  StreamSubscription<String>? _back;

  @override
  void initState() {
    super.initState();
    if (widget.data.messages == null) widget.data.open();
    _text.addListener(() => setState(() {}));
    _back = widget.data.givenBack.listen((w) {
      if (_text.text.isEmpty) _text.text = w;
    });
  }

  @override
  void dispose() {
    _back?.cancel();
    _text.dispose();
    super.dispose();
  }

  void _send() {
    final words = _text.text;
    if (words.trim().isEmpty) return;
    _text.clear();
    widget.data.send(words);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final pad = MediaQuery.paddingOf(context);
    final d = widget.data;
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: dark ? SystemUiOverlayStyle.light : SystemUiOverlayStyle.dark,
      child: ListenableBuilder(
        listenable: d,
        builder: (context, _) {
          final c = d.chat;
          final note = c == null ? null : replyNote(c);
          final canSend = c != null && !c.stopped && c.phone.isNotEmpty;
          final tooLong = c != null && lateTooLong(_text.text, c.windowOpen);
          final owes = c == null ? '' : owesWords(c.owesKobo, c.openInvoices);
          final title = c == null ? 'Customer' : customerTitle(c);
          return Semantics(
            label: 'Conversation with ${c == null ? 'a customer' : title}',
            container: true,
            child: CustomPaint(
              painter: Wallpaper(t.chatWallpaper, t.chatDoodle),
              child: Column(
                children: [
                  ColoredBox(
                    color: t.panel,
                    child: Padding(
                      padding: EdgeInsets.only(top: pad.top),
                      child: ChatHeadBar(
                        onBack: widget.onBack,
                        // CustomerChat.tsx's menu: Archive, or Unarchive.
                        onMore: c == null
                            ? null
                            : (at) => showHeadMenu(
                                context,
                                at: at,
                                items: [
                                  (
                                    c.archived ? 'Unarchive' : 'Archive',
                                    () async {
                                      final ok = await d.setArchived(!c.archived);
                                      if (!ok || !context.mounted) return;
                                      showTkToast(context, c.archived ? 'Brought back to the chats' : 'Archived');
                                      // Archived, it leaves the open chat for the list.
                                      if (!c.archived) (widget.onBack ?? () => Navigator.of(context).maybePop())();
                                    },
                                  ),
                                ],
                              ),
                        avatar: ChatAvatar(name: c == null ? 'Customer' : customerLetter(c), size: 42, letterSize: 15),
                        title: title,
                        subtitle: c == null
                            ? null
                            : [if (c.phone.isNotEmpty && c.phone != c.name) phoneWords(c.phone), owes, 'Customer on WhatsApp'].where((s) => s.isNotEmpty).join(' · '),
                      ),
                    ),
                  ),
                  Expanded(
                    child: ThreadView<CustomerLine>(
                      items: d.messages,
                      of: (id: (m) => m.id, at: (m) => m.m.createdAt, mine: (m) => m.m.mine),
                      now: d.chats.now(),
                      hasOlder: d.hasOlder,
                      onOlder: d.older,
                      errors: [?d.error],
                      empty: Text(
                        c?.stopped == true
                            ? 'What they wrote was deleted when they sent STOP.'
                            : 'Nothing here yet. What this customer writes to your WhatsApp number shows here.',
                        textAlign: TextAlign.center,
                        style: tkHint(t),
                      ),
                      render: (m, prev) {
                        final first = prev == null || prev.m.mine != m.m.mine || prev.m.senderName != m.m.senderName;
                        return Padding(padding: EdgeInsets.only(top: first ? 6 : 0), child: _CustomerBubble(x: m, first: first));
                      },
                    ),
                  ),
                  // CustomerOrders.tsx: an order started from the conversation (M7
                  // builds what it opens).
                  if (canSend)
                    Padding(
                      padding: const EdgeInsets.fromLTRB(12, 0, 12, 8),
                      child: Align(
                        alignment: Alignment.centerLeft,
                        child: _StartOrder(onTap: () {}),
                      ),
                    ),
                  if (note != null) _Note(text: note.text, stop: note.stop),
                  if (canSend && tooLong)
                    _Note(
                      stop: true,
                      text: 'Too long to send now: after a day, a reply can be up to $lateReplyMax characters. This one is ${lateLength(_text.text)}.',
                    ),
                  if (canSend) _ReplyBox(text: _text, onSend: d.busy || tooLong ? null : _send, bottom: pad.bottom, label: 'Reply to $title'),
                ],
              ),
            ),
          );
        },
      ),
    );
  }
}

/// A customer's message, or the business's reply with who sent it, and how
/// far the reply got.
class _CustomerBubble extends StatelessWidget {
  const _CustomerBubble({required this.x, required this.first});
  final CustomerLine x;
  final bool first;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final m = x.m;
    final meta = m.mine ? t.chatOutMeta : t.muted;
    final stamp = Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        if (m.mine && m.sentAs == 'template') ...[
          Semantics(
            label: 'Sent as a WhatsApp message they answer to carry on, more than a day after they last wrote',
            excludeSemantics: true,
            child: Text('as a message to answer ·', style: tkText(size: 11, lineHeight: 16 / 11, color: meta)),
          ),
          const SizedBox(width: 4),
        ],
        Text(clock(m.createdAt), style: tkText(size: 11, lineHeight: 16 / 11, color: meta)),
        if (m.mine) ...[const SizedBox(width: 4), _Delivery(x: x, meta: meta)],
      ],
    );
    return BubbleFrame(
      mine: m.mine,
      first: first,
      stamp: stamp,
      children: [
        if (m.mine && first && (m.senderName ?? '').isNotEmpty) Text(m.senderName!, style: tkText(size: 12, weight: 600, color: t.tintText)),
        Text.rich(
          TextSpan(
            children: [
              // CustomerChat.tsx keeps no room for the time (no `.stamp-gap`):
              // it sits over the end of the last line, as there.
              TextSpan(text: m.body, style: m.kind == 'media' ? const TextStyle(fontStyle: FontStyle.italic) : null),
            ],
          ),
          style: tkText(size: 16, lineHeight: 23 / 16, color: m.kind == 'media' ? t.muted : t.text),
        ),
        if (m.mine && m.delivery == 'failed')
          Text('Not delivered${(m.failure ?? '').isNotEmpty ? ': ${m.failure}' : '.'}', style: tkText(size: 12, weight: 600, color: t.danger)),
      ],
    );
  }
}

/// DeliveryMark: a clock while it goes, one tick once WhatsApp has it, two
/// once on their phone (blue once read), a mark when it could not be.
class _Delivery extends StatelessWidget {
  const _Delivery({required this.x, required this.meta});
  final CustomerLine x;
  final Color meta;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final d = x.m.delivery;
    if (x.local || d == 'sending') return Semantics(label: 'Sending', child: TkIcon('clock', size: 16, stroke: 2, color: meta));
    return switch (d) {
      'failed' => Semantics(label: 'Not delivered', child: Text('!', style: tkText(size: 11, weight: 700, color: t.danger))),
      'delivered' => Semantics(label: 'Delivered', child: TkIcon('tick-double', size: 16, stroke: 2, color: meta)),
      'read' => Semantics(label: 'Read', child: TkIcon('tick-double', size: 16, stroke: 2, color: t.chatTickRead)),
      _ => Semantics(label: 'Sent', child: TkIcon('tick', size: 16, stroke: 2, color: meta)),
    };
  }
}

/// `.customer-note`: how a reply goes (warn), or that it cannot (stop).
class _Note extends StatelessWidget {
  const _Note({required this.text, required this.stop});
  final String text;
  final bool stop;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      liveRegion: true,
      child: Container(
        margin: const EdgeInsets.fromLTRB(12, 0, 12, 8),
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        decoration: BoxDecoration(color: stop ? t.tkDangerBg : t.tkWarnBg, borderRadius: BorderRadius.circular(12)),
        child: Text(text, style: tkText(size: 13, lineHeight: 1.4, color: stop ? t.tkDangerInk : t.tkWarnInk)),
      ),
    );
  }
}

/// `.small.customer-order-start`: the web's plain small button with a +.
class _StartOrder extends StatelessWidget {
  const _StartOrder({required this.onTap});
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      button: true,
      label: 'Start an order',
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: Container(
          constraints: const BoxConstraints(minHeight: 40),
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
          decoration: BoxDecoration(color: t.panel, border: Border.all(color: t.borderStrong), borderRadius: BorderRadius.circular(8)),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              TkIcon('plus', size: 14, color: t.text),
              const SizedBox(width: 6),
              Text('Start an order', style: tkText(size: 11.9, weight: 500, lineHeight: 1.2, color: t.text)),
            ],
          ),
        ),
      ),
    );
  }
}

/// CustomerPane's box: the round box alone, and Send.
class _ReplyBox extends StatelessWidget {
  const _ReplyBox({required this.text, required this.onSend, required this.bottom, required this.label});
  final TextEditingController text;
  final VoidCallback? onSend;
  final double bottom;
  final String label;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    const lift = [BoxShadow(color: Color(0x140E1A14), offset: Offset(0, 1), blurRadius: 2)];
    final ready = onSend != null && text.text.trim().isNotEmpty;
    return Container(
      color: t.tkSoft,
      padding: EdgeInsets.fromLTRB(10, 8, 10, 12 + bottom),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          Expanded(
            child: Container(
              constraints: const BoxConstraints(minHeight: 46),
              padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
              decoration: BoxDecoration(color: t.panel, borderRadius: BorderRadius.circular(24), boxShadow: lift),
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 10),
                child: Material(
                  type: MaterialType.transparency,
                  child: Semantics(
                    label: label,
                    textField: true,
                    child: TextField(
                      controller: text,
                      minLines: 1,
                      maxLines: 6,
                      maxLength: 2000,
                      buildCounter: (_, {required currentLength, required isFocused, maxLength}) => null,
                      textCapitalization: TextCapitalization.sentences,
                      keyboardType: TextInputType.multiline,
                      cursorColor: t.text,
                      showCursor: !TkField.debugHideCaret,
                      style: tkText(size: 15.5, lineHeight: 1.4, color: t.text),
                      decoration: InputDecoration.collapsed(hintText: 'Reply', hintStyle: tkText(size: 15.5, lineHeight: 1.4, color: t.muted)),
                    ),
                  ),
                ),
              ),
            ),
          ),
          const SizedBox(width: 8),
          // `.send-button:disabled` keeps its look.
          Semantics(
            button: true,
            enabled: ready,
            label: 'Send',
            child: TkPress(
              onTap: ready ? onSend : null,
              child: Container(
                width: 46,
                height: 46,
                decoration: BoxDecoration(color: dark ? t.brandDark : t.brand, shape: BoxShape.circle),
                alignment: Alignment.center,
                child: TkIcon('arrow-up', size: 22, stroke: 2.4, color: dark ? t.brandDarkText : t.brandDark),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
