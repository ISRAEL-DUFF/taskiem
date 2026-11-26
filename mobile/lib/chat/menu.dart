// Holding a message, as WhatsApp's on a phone (the founder's feedback of 25
// November 2026: the old menu's card of actions lay over its reactions on
// a long message). A message held is selected: its row tinted across the
// thread, the chat's head turned into the selection's bar (✕ and how many,
// then Reply, Star, Delete, Forward and ⋮ for the rest), and the reactions
// floating on their own over the message, or under it with no room above,
// kept on the screen however long it is. While any is held, a tap on
// another adds it or takes it away; with several, only what may be done
// with all of them is offered. Back, ✕, the last one let go or anything
// done ends it.

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/tap_area.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'emoji_panel.dart';
import 'thread.dart' show ChatHeadBar, showHeadMenu;

/// linkchat.ts REACTIONS, and Conversation.tsx's names for them.
const reactions = ['👍', '❤️', '😂', '😮', '😢', '🙏'];
const reactionNames = {'👍': 'thumbs up', '❤️': 'heart', '😂': 'laughing', '😮': 'surprise', '😢': 'sad', '🙏': 'thanks'};

/// Something more to do with one held message (Save as…, Who reacted…):
/// under the selection bar's ⋮.
class MenuItem {
  const MenuItem(this.icon, this.label, this.onTap, {this.danger = false, this.separated = false, this.done});
  final String icon;
  final String label;
  final VoidCallback onTap;
  final bool danger;

  /// What a toast says once it is done (Copy's "Copied").
  final String? done;

  /// Kept for the callers' grouping; the ⋮ menu draws no lines.
  final bool separated;
}

/// What may be done with one message, by the same rules the chat's menu
/// had (who may write, Edit's rules in edits.dart, a card or a deleted
/// message offering less). Null where it may not be done.
class HeldActs {
  const HeldActs({
    required this.what,
    required this.mine,
    this.words = '',
    this.onReact,
    this.mineEmoji,
    this.reply,
    this.star,
    this.starred = false,
    this.forward = false,
    this.delete = false,
    this.edit,
    this.info,
    this.infoLabel = 'Message info',
    this.more = const [],
  });

  /// The message in a few words, for a screen reader.
  final String what;
  final bool mine;

  /// Its words to copy ('' for none).
  final String words;

  /// A reaction given (null: none may be).
  final ValueChanged<String>? onReact;

  /// The reaction already ours.
  final String? mineEmoji;
  final VoidCallback? reply;

  /// Starred or unstarred (the one action).
  final VoidCallback? star;
  final bool starred;

  /// May go with Forward or Delete (done for all held by the selection's
  /// [MessageSelection.onForward] and [MessageSelection.onDelete]).
  final bool forward;
  final bool delete;
  final VoidCallback? edit;
  final VoidCallback? info;
  final String infoLabel;

  /// The rest, for one message only (Save as…, a photo's reading).
  final List<MenuItem> more;
}

/// One thing the selection bar offers.
class HeldAction {
  const HeldAction(this.label, this.icon, this.run, {this.bar = false, this.said});
  final String label;
  final String icon;
  final VoidCallback run;

  /// On the bar itself, not under ⋮.
  final bool bar;

  /// A toast once done.
  final String? said;
}

/// The messages held in a chat, in the order they were held.
class MessageSelection extends ChangeNotifier {
  MessageSelection({required this.actsOf, this.onDelete, this.onForward});

  /// What may be done with a message; null for one that cannot be held.
  final HeldActs? Function(String id) actsOf;

  /// Delete for all of [ids]: the chat's choices ("Delete for me", and
  /// "Delete for everyone" only when each may be).
  final Future<void> Function(List<String> ids)? onDelete;

  /// Forward all of [ids], in the thread's order.
  final Future<void> Function(List<String> ids)? onForward;

  /// The thread's messages in order (set by the thread).
  List<String> order = const [];

  /// Where a message's bubble is, relative to [ancestor], while drawn (set
  /// by the thread).
  Rect? Function(String id, RenderObject? ancestor)? locate;

  final List<String> _ids = [];

  bool get on => _ids.isNotEmpty;
  int get count => _ids.length;
  bool has(String id) => _ids.contains(id);

  /// Those held, in the thread's order.
  List<String> get ids {
    final at = {for (var i = 0; i < order.length; i++) order[i]: i};
    return [..._ids]..sort((a, b) => (at[a] ?? -1).compareTo(at[b] ?? -1));
  }

  /// A finger held on a message: selects it (or, while selecting, adds it
  /// or takes it away).
  void hold(String id) {
    if (on) return toggle(id);
    if (actsOf(id) == null) return;
    HapticFeedback.selectionClick();
    _ids.add(id);
    notifyListeners();
  }

  /// A tap on a message while selecting.
  void toggle(String id) {
    if (_ids.remove(id)) {
      notifyListeners();
      return;
    }
    if (actsOf(id) == null) return;
    _ids.add(id);
    notifyListeners();
  }

  void clear() {
    if (_ids.isEmpty) return;
    _ids.clear();
    notifyListeners();
  }

  /// The one held, with what may be done with it; null with none or
  /// several.
  ({String id, HeldActs acts})? get single {
    if (_ids.length != 1) return null;
    final a = actsOf(_ids.single);
    return a == null ? null : (id: _ids.single, acts: a);
  }

  /// What the bar offers for those held: Reply, Star, Delete and Forward
  /// on it, the rest under ⋮; with several, only what may be done with
  /// all of them (Star, Delete, Forward, Copy).
  List<HeldAction> actions() {
    final ids = this.ids;
    final all = [for (final id in ids) ?actsOf(id)];
    if (all.isEmpty || all.length != ids.length) return const [];
    final one = all.length == 1 ? all.single : null;
    // Anything done ends the selection; then it is done.
    VoidCallback then(VoidCallback f) => () {
      clear();
      f();
    };
    final starred = all.every((a) => a.starred);
    return [
      if (one?.reply != null) HeldAction('Reply', 'reply', then(one!.reply!), bar: true),
      if (all.every((a) => a.star != null))
        HeldAction(starred ? 'Unstar' : 'Star', 'star', then(() {
          // Several: those not yet starred are; all starred, all unstarred.
          for (final a in all) {
            if (starred || !a.starred) a.star!();
          }
        }), bar: true),
      if (onDelete != null && all.every((a) => a.delete)) HeldAction('Delete', 'trash', then(() => onDelete!(ids)), bar: true),
      if (onForward != null && all.every((a) => a.forward)) HeldAction('Forward', 'forward', then(() => onForward!(ids)), bar: true),
      if (all.every((a) => a.words.isNotEmpty))
        HeldAction(
          'Copy',
          'copy',
          then(() => Clipboard.setData(ClipboardData(text: [for (final a in all) a.words].join('\n')))),
          said: all.length == 1 ? 'Copied' : '${all.length} messages copied',
        ),
      if (one?.edit != null) HeldAction('Edit', 'pencil', then(one!.edit!)),
      if (one?.info != null) HeldAction(one!.infoLabel, 'info', then(one.info!)),
      if (one != null)
        for (final i in one.more) HeldAction(i.label, i.icon, then(i.onTap), said: i.done),
    ];
  }

  /// A reaction chosen for the one held.
  void react(String emoji) {
    final s = single;
    if (s == null || s.acts.onReact == null) return;
    clear();
    s.acts.onReact!(emoji);
  }
}

/// The chat's head, or while messages are held the selection's bar in its
/// place (the head kept under it, so nothing below moves).
class SelectionHead extends StatelessWidget {
  const SelectionHead({super.key, required this.selection, required this.child});
  final MessageSelection selection;
  final Widget child;

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: selection,
    builder: (context, head) {
      final on = selection.on && selection.actions().isNotEmpty;
      return Stack(
        children: [
          Visibility(visible: !on, maintainSize: true, maintainAnimation: true, maintainState: true, child: head!),
          if (on) Positioned.fill(child: SelectionBar(selection: selection)),
        ],
      );
    },
    child: child,
  );
}

/// WhatsApp's selection bar: ✕ and how many are held, then Reply, Star,
/// Delete and Forward, and ⋮ with the rest.
class SelectionBar extends StatelessWidget {
  const SelectionBar({super.key, required this.selection});
  final MessageSelection selection;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final all = selection.actions();
    final n = selection.count;
    void run(HeldAction a) {
      a.run();
      if (a.said != null) showTkToast(context, a.said!);
    }

    final rest = all.where((a) => !a.bar).toList();
    return Container(
      padding: const EdgeInsets.fromLTRB(8, 6, 6, 8),
      decoration: BoxDecoration(color: t.panel, border: Border(bottom: BorderSide(color: look.line))),
      child: Row(
        children: [
          ChatHeadBar.headButton('Close the selection', TkIcon('b-x', size: 24, stroke: 2.2, color: t.text), selection.clear),
          const SizedBox(width: 8),
          Expanded(
            child: Semantics(
              liveRegion: true,
              label: n == 1 ? '1 message selected' : '$n messages selected',
              excludeSemantics: true,
              child: Text('$n', maxLines: 1, style: tkDisplay(size: 19, lineHeight: 1.25, letterSpacing: -0.3, color: t.text)),
            ),
          ),
          for (final a in all.where((a) => a.bar)) ...[
            const SizedBox(width: 2),
            ChatHeadBar.headButton(a.label, TkIcon(a.icon, size: 22, stroke: 1.9, color: t.text, fill: false), () => run(a)),
          ],
          if (rest.isNotEmpty) ...[
            const SizedBox(width: 2),
            Builder(
              builder: (context) => ChatHeadBar.headButton(
                'More options',
                TkIcon('b-kebab', size: 22, stroke: 2.4, color: t.text),
                () {
                  final box = context.findRenderObject()! as RenderBox;
                  showHeadMenu(context, at: box.localToGlobal(Offset.zero) & box.size, items: [for (final a in rest) (a.label, () => run(a))]);
                },
              ),
            ),
          ],
        ],
      ),
    );
  }
}

/// Round the thread: Back ends the selection, and the reactions float
/// over the one message held.
class SelectionScope extends StatefulWidget {
  const SelectionScope({super.key, required this.selection, required this.child});
  final MessageSelection selection;
  final Widget child;

  @override
  State<SelectionScope> createState() => _SelectionScopeState();
}

class _SelectionScopeState extends State<SelectionScope> {
  final _area = GlobalKey();

  /// Told when the thread moves: the reactions follow their message.
  final _moved = ValueNotifier<int>(0);

  /// The "+" chosen: every emoji in the reactions' place.
  bool _picking = false;

  @override
  void initState() {
    super.initState();
    widget.selection.addListener(_changed);
  }

  @override
  void didUpdateWidget(SelectionScope old) {
    super.didUpdateWidget(old);
    if (old.selection != widget.selection) {
      old.selection.removeListener(_changed);
      widget.selection.addListener(_changed);
    }
  }

  @override
  void dispose() {
    widget.selection.removeListener(_changed);
    _moved.dispose();
    super.dispose();
  }

  void _changed() => setState(() => _picking = false);

  Rect? _find(String id) {
    final area = _area.currentContext?.findRenderObject();
    return widget.selection.locate?.call(id, area);
  }

  @override
  Widget build(BuildContext context) {
    final sel = widget.selection;
    final one = sel.single;
    final react = one?.acts.onReact != null;
    return PopScope(
      canPop: !sel.on,
      onPopInvokedWithResult: (popped, _) {
        if (!popped) sel.clear();
      },
      child: NotificationListener<ScrollNotification>(
        onNotification: (_) {
          if (react) _moved.value++;
          return false;
        },
        child: Stack(
          key: _area,
          children: [
            Positioned.fill(child: widget.child),
            if (one != null && one.acts.onReact != null)
              Positioned.fill(
                child: CustomSingleChildLayout(
                  delegate: _Over(find: () => _find(one.id), mine: one.acts.mine, moved: _moved),
                  child: _picking
                      ? _Picker(onPick: sel.react)
                      : _Reactions(
                          mine: one.acts.mineEmoji,
                          onPick: sel.react,
                          onMore: () => setState(() => _picking = true),
                        ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

/// BubbleShell's placing: 6 px above the bubble, or under it with no room
/// above, against the bubble's own side, kept 8 px inside the thread; with
/// room neither above nor below (a long message), at the top of the
/// thread over the message. Away while the message is out of sight.
class _Over extends SingleChildLayoutDelegate {
  _Over({required this.find, required this.mine, required Listenable moved}) : super(relayout: moved);
  final Rect? Function() find;
  final bool mine;

  @override
  BoxConstraints getConstraintsForChild(BoxConstraints constraints) => BoxConstraints.loose(Size(constraints.maxWidth - 16, (constraints.maxHeight - 16).clamp(0.0, double.infinity)));

  @override
  Offset getPositionForChild(Size size, Size child) {
    final r = find();
    const gap = 6.0, edge = 8.0;
    // Out of sight: out of the way (drawn, but off the thread).
    if (r == null || r.bottom < 0 || r.top > size.height) return Offset(0, size.height + 1000);
    final most = (size.width - child.width - edge).clamp(edge, double.infinity);
    final x = (mine ? r.right - child.width : r.left).clamp(edge, most);
    var y = r.top - child.height - gap;
    if (y < edge) {
      y = r.bottom + gap;
      if (y + child.height > size.height - edge) y = (r.top - child.height - gap).clamp(edge, (size.height - child.height - edge).clamp(edge, double.infinity));
    }
    return Offset(x, y);
  }

  @override
  bool shouldRelayout(_Over old) => true;
}

/// `.pop-pill`: the reactions in a pill, "+" for any emoji.
class _Reactions extends StatelessWidget {
  const _Reactions({required this.mine, required this.onPick, required this.onMore});
  final String? mine;
  final ValueChanged<String> onPick;
  final VoidCallback onMore;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      container: true,
      label: 'React',
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
        decoration: BoxDecoration(
          color: t.panel,
          borderRadius: BorderRadius.circular(999),
          boxShadow: [BoxShadow(color: t.chatShadow, offset: const Offset(0, 2), blurRadius: 12)],
        ),
        child: FittedBox(
          fit: BoxFit.scaleDown,
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              for (final e in reactions) ...[
                _Pick(emoji: e, label: 'React with ${reactionNames[e]}', pressed: mine == e, onTap: () => onPick(e)),
                const SizedBox(width: 2),
              ],
              // "+": every emoji.
              TapArea(
                child: Semantics(
                  container: true,
                  button: true,
                  label: 'More reactions',
                  onTap: onMore,
                  excludeSemantics: true,
                  child: TkPress(
                    onTap: onMore,
                    child: Container(
                      width: 38,
                      height: 38,
                      decoration: BoxDecoration(color: t.chatSelect, shape: BoxShape.circle),
                      alignment: Alignment.center,
                      child: TkIcon('plus', size: 20, color: t.muted),
                    ),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// `.pop-picker { width: min(360px, calc(100vw - 16px)); border-radius: 14px }`
class _Picker extends StatelessWidget {
  const _Picker({required this.onPick});
  final ValueChanged<String> onPick;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return LayoutBuilder(
      builder: (context, box) => Container(
        width: box.maxWidth.clamp(0.0, 360.0),
        clipBehavior: Clip.antiAlias,
        decoration: BoxDecoration(
          color: t.panel,
          borderRadius: BorderRadius.circular(14),
          boxShadow: [BoxShadow(color: t.chatShadow, offset: const Offset(0, 4), blurRadius: 18)],
        ),
        child: EmojiPanel(maxHeight: box.maxHeight.clamp(0.0, 330.0), onPick: onPick),
      ),
    );
  }
}

/// `.pop-pill .react-pick`: an emoji, 38 px round, ringed when it is ours.
class _Pick extends StatelessWidget {
  const _Pick({required this.emoji, required this.label, required this.pressed, required this.onTap});
  final String emoji;
  final String label;
  final bool pressed;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TapArea(child: Semantics(
      button: true,
      selected: pressed,
      label: label,
      onTap: onTap,
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: Container(
          width: 38,
          height: 38,
          decoration: BoxDecoration(
            color: pressed ? t.chatSelect : null,
            shape: BoxShape.circle,
            border: pressed ? Border.all(color: t.accent, width: 2) : null,
          ),
          alignment: Alignment.center,
          child: Text(emoji, style: tkText(size: 24, lineHeight: 1)),
        ),
      ),
    ));
  }
}
