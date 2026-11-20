// A saved thing shared in a chat (saved objects S1, part 3; saved-objects
// §6.2): a copy of it as it was sent, folded to a row (its kind's tile,
// its title, what it is), opening to each detail with Copy beside it, and
// Save this for whoever received it, as a saved thing of their own. And
// sending one: once, then again with the sender's yes when the server asks
// for one before sharing what is sensitive (409 confirm_share).

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'cards.dart' show CardButton;
import 'groups.dart' show askSure;
import 'saved_accounts.dart';

/// Under a card's title: the kind, and the first detail that is not the
/// title ("Bank account · Guaranty Trust Bank").
String savedCardSub(SavedCard c) {
  final kind = switch (c.kind) {
    'bank_account' => 'Bank account',
    'person' => 'Person',
    'place' => 'Place',
    'text' => 'Text',
    'provider_account' => 'Account with a provider',
    // A made-up kind's own name ("Car").
    'custom' => c.kindName ?? 'Saved',
    _ => 'Saved',
  };
  final first = c.lines.isEmpty ? '' : c.lines.first.value.split('\n').first;
  return [kind, if (first.isNotEmpty && c.kind != 'text') first].join(' · ');
}

class SavedCardView extends StatefulWidget {
  const SavedCardView({super.key, required this.card, required this.mine, this.onSave, this.ink});
  final SavedCard card;

  /// Sent by the one reading it: no Save this.
  final bool mine;

  /// Save this (saveSavedCard); none, no button.
  final VoidCallback? onSave;

  /// The words' colour on the bubble; none, the theme's.
  final Color? ink;

  @override
  State<SavedCardView> createState() => _SavedCardViewState();
}

class _SavedCardViewState extends State<SavedCardView> {
  bool _open = false;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final c = widget.card;
    final ink = widget.ink ?? t.text;
    final muted = widget.ink == null ? t.muted : ink.withValues(alpha: 0.75);
    final icon = c.kind == 'custom' ? 'b-box' : (savedKinds[c.kind]?.icon ?? 'b-note');
    final head = Semantics(
      button: true,
      label: '${c.title}, shared. ${savedCardSub(c)}. ${_open ? 'Show less' : 'Show details'}',
      excludeSemantics: true,
      onTap: () => setState(() => _open = !_open),
      child: TkPress(
        onTap: () => setState(() => _open = !_open),
        child: ConstrainedBox(
          constraints: const BoxConstraints(minHeight: 48),
          child: Row(
            children: [
              Container(
                width: 36,
                height: 36,
                alignment: Alignment.center,
                decoration: BoxDecoration(color: look.ground, borderRadius: BorderRadius.circular(10)),
                child: TkIcon(icon, size: 19, color: t.text),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(c.title, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 15, weight: 600, lineHeight: 1.3, color: ink)),
                    Text(_open ? 'Shared as @${c.name}' : savedCardSub(c), maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 12.5, lineHeight: 1.35, color: muted)),
                  ],
                ),
              ),
              const SizedBox(width: 6),
              RotatedBox(quarterTurns: _open ? 3 : 1, child: TkIcon('chevron', size: 16, color: muted)),
            ],
          ),
        ),
      ),
    );
    return Semantics(
      container: true,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          head,
          if (_open) ...[
            const SizedBox(height: 6),
            for (final l in c.lines) _Line(line: l, ink: ink, muted: muted, copy: !(widget.mine && l.sensitive == true)),
            if (!widget.mine && widget.onSave != null) ...[const SizedBox(height: 10), CardButton(label: 'Save this', primary: true, onTap: widget.onSave)],
          ],
        ],
      ),
    );
  }
}

class _Line extends StatelessWidget {
  const _Line({required this.line, required this.ink, required this.muted, this.copy = true});
  final SavedCardLinesItem line;
  final Color ink;
  final Color muted;

  /// Copy beside it; not on one's own masked line (nothing there to copy).
  final bool copy;

  @override
  Widget build(BuildContext context) {
    final text = line.label == 'Text';
    final what = text ? 'the text' : line.label.toLowerCase();
    return Row(
      children: [
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              if (!text) Text(line.label, style: tkText(size: 12, lineHeight: 1.3, color: muted)),
              Text(line.value, style: tkText(size: 14, weight: 600, lineHeight: 1.35, color: ink)),
            ],
          ),
        ),
        if (copy)
        Semantics(
          button: true,
          label: 'Copy $what',
          excludeSemantics: true,
          child: TkPress(
            onTap: () async {
              await Clipboard.setData(ClipboardData(text: line.value));
              if (context.mounted) showTkToast(context, '${what[0].toUpperCase()}${what.substring(1)} copied');
            },
            child: SizedBox(width: 48, height: 48, child: Center(child: TkIcon('copy', size: 18, color: muted))),
          ),
        ),
      ],
    );
  }
}

/// Save this: a card shared with the person, saved as their own (or, read
/// in a business's chat, [mine] false, the business's), in the sheet that
/// saves one, filled from the card to change before saving.
Future<void> saveSavedCard(BuildContext context, Auth auth, SavedCard c, {bool mine = true}) async {
  // The business's only for whoever may manage its saved things.
  mine = mine || !auth.can('saved.manage');
  final api = SavedApi(auth, mine);
  final done = c.kind == 'bank_account'
      ? (await showTkSheet<String>(context, (_) => SavedAccountSheet(api: api, checks: false, start: c))) != null
      // A made-up kind's card is kept as a text of its lines: the receiver
      // has no such kind.
      : (await showTkSheet<bool>(context, (_) => SavedThingSheet(api: api, kind: c.kind == 'custom' ? 'text' : c.kind, start: c))) == true;
  if (done && context.mounted) showTkToast(context, mine ? 'Saved as yours' : 'Saved for the business');
}

/// Sends a saved thing as a card: once, and when the server asks for a yes
/// to sharing what is sensitive in it, [ask]s in its words ("Share Mum's
/// account details with them?") and sends again with it. Null when the
/// sender said no.
Future<T?> sendSaved<T>(Future<T> Function(bool confirm) send, Future<bool> Function(String question) ask) async {
  try {
    return await send(false);
  } on ApiError catch (e) {
    if (e.status != 409 || !e.has('confirm_share')) rethrow;
    return await ask(e.message) ? send(true) : null;
  }
}

/// The sender asked, in a sheet, before sharing what is sensitive.
Future<bool> Function(String question) askShare(BuildContext context) =>
    (question) async => context.mounted && await askSure(context, title: question, ok: 'Share');
