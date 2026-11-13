// Messages about it (web Conversation.tsx DocThread): a document's thread
// under its steps, the cards about it and the replies to them, newest
// last; read again every 10 seconds while seen; for those who may trade
// (chat.trade), a box to reply to its latest card. Nothing shows until
// the thread has a card. GET /v1/link-chats/thread?doc=…&reference=…,
// POST /v1/link-chats/{link}/messages with text and reply_to.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../app/session.dart' show uuid4;
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/page.dart';
import '../kit/polling.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'cards.dart' show CardFrame;
import 'words.dart' show cardLine, clock;

/// The thread of one document: an order (ours), a purchase, an invoice or
/// a bill.
class DocThread extends StatefulWidget {
  const DocThread({super.key, required this.auth, required this.doc, required this.reference, this.now, this.follow = true});
  final Auth auth;
  final String doc;
  final String reference;
  final DateTime Function()? now;

  /// Read again every 10 seconds (the web's EVERY_MS × 2).
  final bool follow;

  @override
  State<DocThread> createState() => _DocThreadState();
}

class _DocThreadState extends State<DocThread> with Polling {
  List<LinkMessage>? _list;
  final _text = TextEditingController();
  bool _busy = false;
  String? _error;
  int _asked = 0;

  /// One reply, one id: sent again after a dropped answer, it is not sent
  /// twice (the server answers the first).
  String _clientId = uuid4();

  @override
  void initState() {
    super.initState();
    _text.addListener(() => setState(() {}));
    _load();
    if (widget.follow) startPolling(const Duration(seconds: 10), _load);
  }

  @override
  void dispose() {
    _text.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final asked = ++_asked;
    try {
      final r = await widget.auth.transport.send('GET', '/v1/link-chats/thread', query: {'doc': widget.doc, 'reference': widget.reference});
      final list = [
        for (final m in ((r as Map)['messages'] as List? ?? const []).whereType<Map<String, dynamic>>()) LinkMessage.fromJson(m),
      ];
      if (mounted && asked == _asked) setState(() => _list = list);
    } on Object {
      // Nothing about it to show: the screen is the order's, not the thread's.
    }
  }

  Future<void> _send(LinkMessage anchor) async {
    final words = _text.text.trim();
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send(
        'POST',
        '/v1/link-chats/${Uri.encodeComponent(anchor.link)}/messages',
        body: {'text': words, 'reply_to': anchor.id, 'client_id': _clientId},
      );
      if (!mounted) return;
      _text.clear();
      _clientId = uuid4();
      showTkToast(context, 'Sent');
      await _load();
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final list = _list ?? const <LinkMessage>[];
    final cards = list.where((m) => m.kind == 'card' && m.card != null).toList();
    if (cards.isEmpty) return const SizedBox.shrink();
    final anchor = cards.last;
    final now = (widget.now ?? DateTime.now)();
    return TkPageCard(
      title: 'Messages about it',
      children: [
        for (final m in list) Padding(padding: const EdgeInsets.only(bottom: 8), child: _Line(m: m, now: now)),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 4, bottom: 8), child: TkErrorBox(_error!)),
        if (widget.auth.can('chat.trade')) ...[
          const SizedBox(height: 4),
          Row(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              Expanded(
                child: Semantics(
                  label: 'Reply about ${widget.reference}',
                  child: TkTextBox(controller: _text, hint: 'Reply about ${widget.reference}', height: 44, size: 15, max: 4000),
                ),
              ),
              const SizedBox(width: 8),
              TkSmallButton(
                label: 'Send',
                semantics: 'Send the reply about ${widget.reference}',
                primary: true,
                onPressed: _busy || _text.text.trim().isEmpty ? null : () => _send(anchor),
              ),
            ],
          ),
        ],
      ],
    );
  }
}

/// One message of the thread: a card as the conversation draws it (its
/// buttons are this screen's), or a bubble of words; theirs on the left,
/// ours on the right, its time under.
class _Line extends StatelessWidget {
  const _Line({required this.m, required this.now});
  final LinkMessage m;
  final DateTime now;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final mine = m.mine;
    final card = m.card;
    final a = m.attachment;
    final words = (m.body ?? '').trim();
    final stamp = Text(clock(m.createdAt), style: tkText(size: 11, color: t.muted));
    final Widget inside = card != null
        ? CardFrame(card: card, title: cardLine(card, mine, 'They'), mine: mine, now: now, actions: const [])
        : Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (!mine && (m.senderName ?? '').isNotEmpty) Text(m.senderName!, style: tkText(size: 13, weight: 600, color: t.muted)),
              if (a != null)
                Text(
                  a.kind == 'photo' ? 'A photo' : (a.kind == 'voice' ? 'A voice note' : ((a.name ?? '').isEmpty ? 'A file' : a.name!)),
                  style: tkText(size: 15, weight: 600, color: t.text),
                ),
              if (words.isNotEmpty) Text(words, style: tkText(size: 15, lineHeight: 1.45, color: t.text)),
            ],
          );
    return Align(
      alignment: mine ? Alignment.centerRight : Alignment.centerLeft,
      child: ConstrainedBox(
        constraints: BoxConstraints(maxWidth: card != null ? double.infinity : 300),
        child: Container(
          padding: card != null ? const EdgeInsets.all(6) : const EdgeInsets.fromLTRB(10, 7, 10, 6),
          decoration: BoxDecoration(
            color: mine ? t.chatOut : t.chatIn,
            borderRadius: BorderRadius.circular(14),
            border: Border.all(color: t.border),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            mainAxisSize: MainAxisSize.min,
            children: [
              inside,
              const SizedBox(height: 2),
              Align(alignment: Alignment.centerRight, child: stamp),
            ],
          ),
        ),
      ),
    );
  }
}
