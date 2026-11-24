// A finger's room (docs/spec/mobile-app.md §8): the web draws its round
// buttons 40 px, its chips 34 px, its buttons 36 to 44 px, and the app
// draws them the same. What can be pressed is pressed in at least 48 by 48
// all the same, and a screen reader is given that room: the box is drawn as
// it is, and the space round it, up to 48, answers to a finger too (as
// Material's padded tap targets do, without moving anything).

import 'package:flutter/rendering.dart';
import 'package:flutter/widgets.dart';

/// The least a finger is given, each way (Android's 48; an iPhone's is 44).
const tapMin = 48.0;

/// Makes [child] at least [tapMin] each way to a finger and a screen
/// reader, drawn as it is. Its semantics (a button and its words below it)
/// are one node with that room.
class TapArea extends SingleChildRenderObjectWidget {
  const TapArea({super.key, required super.child});

  @override
  RenderObject createRenderObject(BuildContext context) => _RenderTapArea();
}

class _RenderTapArea extends RenderProxyBox {
  /// The box, grown about its middle to [tapMin] where it is smaller.
  Rect get _room {
    final w = size.width < tapMin ? (tapMin - size.width) / 2 : 0.0;
    final h = size.height < tapMin ? (tapMin - size.height) / 2 : 0.0;
    return Rect.fromLTRB(-w, -h, size.width + w, size.height + h);
  }

  @override
  bool hitTest(BoxHitTestResult result, {required Offset position}) {
    if (!_room.contains(position)) return false;
    // A press in the room round it is a press on it, at its nearest edge.
    final at = Offset(position.dx.clamp(0.0, size.width - 0.01), position.dy.clamp(0.0, size.height - 0.01));
    if (hitTestChildren(result, position: at)) {
      result.add(BoxHitTestEntry(this, position));
      return true;
    }
    return false;
  }

  @override
  Rect get semanticBounds => _room;

  @override
  void describeSemanticsConfiguration(SemanticsConfiguration config) {
    super.describeSemanticsConfiguration(config);
    // One node: the button's words and actions (or a field's words and the
    // field), with the room.
    config.isSemanticBoundary = true;
    config.isMergingSemanticsOfDescendants = true;
  }
}

/// A text field's box: a press anywhere in it (or on its words above, as a
/// web `label` does) puts the caret in the field, and it is one node, at
/// least [tapMin] each way, to a screen reader.
class TapField extends StatelessWidget {
  const TapField({super.key, required this.child});
  final Widget child;

  @override
  Widget build(BuildContext context) => TapArea(
    child: GestureDetector(
      behavior: HitTestBehavior.opaque,
      excludeFromSemantics: true,
      onTap: () => _focusIn(context),
      child: child,
    ),
  );

  static void _focusIn(BuildContext context) {
    EditableTextState? found;
    void look(Element e) {
      if (found != null) return;
      if (e is StatefulElement && e.state is EditableTextState) {
        found = e.state as EditableTextState;
        return;
      }
      e.visitChildElements(look);
    }

    context.visitChildElements(look);
    found?.requestKeyboard();
  }
}

/// The dimmed or clear space round a menu or sheet: a press there closes
/// it, and a screen reader hears "Dismiss" (as Material's barrier is
/// named; a sheet's own button is "Close").
class Backdrop extends StatelessWidget {
  const Backdrop({super.key, required this.onClose, this.child, this.behavior = HitTestBehavior.opaque, this.onVerticalDragStart});
  final VoidCallback onClose;
  final Widget? child;
  final HitTestBehavior behavior;
  final GestureDragStartCallback? onVerticalDragStart;

  @override
  Widget build(BuildContext context) => Semantics(
    button: true,
    label: 'Dismiss',
    child: GestureDetector(behavior: behavior, onTap: onClose, onVerticalDragStart: onVerticalDragStart, child: child),
  );
}

