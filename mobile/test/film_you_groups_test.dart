// A film: a new group made for a purpose (customer series, episode 8,
// you-groups). Tobi Adewale taps New chat, then New group, names it "Family
// lunch" and says what it is for, adds Mama Tobi and his sister Bisi, makes
// the group, sees how each add went, opens the group's info and the tools
// menu (Order together). Set-up from groups_test.dart ("a new group"),
// groups_review_test.dart and people_test.dart. Skipped unless VIDEO_DIR is
// set.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/people.dart' show PersonFace;
import 'package:taskiem/chat/thread.dart' show ChatHeadBar;

import 'film.dart';
import 'film_you_ep78_world.dart';
import 'groups_test.dart' show gm, grp, member;
import 'people_test.dart' show Api, answer, card;
import 'support.dart' show loadFonts;

const _name = 'Family lunch';
const _about = 'Where we pick and order Sunday lunch together';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: a new group for a purpose', (t) async {
    final api = Api();
    youRoutes(api, [personRow(id: 'pc-mama', personId: 'p-mama', name: 'Mama Tobi', last: 'Good morning, Mama')]);
    api.routes['GET /v1/me/contacts'] = (_) => {
      'contacts': [
        {...card(id: 'p-mama', name: 'Mama Tobi'), 'added_at': '2026-11-01T10:00:00Z', 'source': 'added'},
        {...card(id: 'p-bisi', name: 'Bisi Adewale'), 'added_at': '2026-11-01T10:00:00Z', 'source': 'added'},
        {...card(id: 'p-chioma', name: 'Chioma Nnadi'), 'added_at': '2026-11-02T10:00:00Z', 'source': 'added'},
      ],
    };
    final g = {...grp(id: 'g1', name: _name, role: 'admin', members: 2, last: 'Tobi Adewale made the group', lastSender: null), 'description': _about, 'last_mine': true};
    api.routes['POST /v1/me/groups'] = (_) => answer({
      'group': g,
      'added': [
        {'person_id': 'p-mama', 'state': 'added'},
        {'person_id': 'p-bisi', 'state': 'invited'},
      ],
    }, 201);
    api.routes['GET /v1/me/groups/g1'] = (_) => g;
    api.routes['GET /v1/me/groups/g1/messages'] = (_) => {
      'messages': [gm('e1', kind: 'event', body: 'Tobi Adewale made the group', senderId: 'me', sender: 'Tobi Adewale', at: '2026-11-24T13:01:00Z', event: {'type': 'created', 'actor_id': 'me'})],
      'more': false,
    };
    api.routes['GET /v1/me/groups/g1/members'] = (_) => {
      'members': [member('me', 'Tobi Adewale', role: 'admin', you: true), member('p-mama', 'Mama Tobi')],
      'invited': [{'person_id': 'p-bisi', 'name': 'Bisi Adewale', 'at': '2026-11-24T13:01:00Z'}],
      'waiting': <Object>[],
    };
    api.routes['POST /v1/me/groups/g1/read'] = (_) => answer({}, 204);
    final tobi = signedInAs(api, name: 'Tobi Adewale', phone: '+2348031230000');
    await openYouHome(t, tobi);

    final f = Film.start(t, 'you-groups');
    f.say('Tobi wants one place to plan Sunday lunch with his family.', who: 'Tobi · Taskiem for you');
    await f.hold(3.4);
    f.say('On Chats he taps the plus, New chat.');
    await f.tap(find.bySemanticsLabel('New chat').first, then: 1.0);
    await f.settle();
    await f.hold(1.0);
    f.say('He taps New group.');
    await f.tap(find.text('New group'), then: 1.0);
    await f.settle();
    await f.hold(1.0);

    f.say('He names the group Family lunch.');
    final nameBox = find.descendant(of: find.byKey(const ValueKey('group-name')), matching: find.byType(EditableText));
    await f.tap(nameBox, then: 0.2);
    await f.keyboard(true, kind: 'text');
    await f.type(nameBox, _name, perKey: 0.12);
    await f.hold(0.8);
    f.say('In Description he says what the group is for.');
    final aboutBox = find.byType(EditableText).at(1);
    await f.tap(aboutBox, then: 0.2);
    await f.type(aboutBox, _about, perKey: 0.06);
    await f.hold(0.8);
    await f.keyboard(false);
    await f.hold(0.6);

    f.say('He taps Add people and ticks Mama Tobi and Bisi.');
    await t.ensureVisible(find.text('Add people'));
    await t.pump();
    await f.hold(0.6);
    await f.tap(find.text('Add people'), then: 1.0);
    await f.settle();
    await f.hold(0.6);
    await f.tap(find.text('Mama Tobi'), then: 0.6);
    await f.tap(find.text('Bisi Adewale'), then: 1.2);
    f.say('He taps Choose, then Make the group.');
    await f.tap(find.text('Choose (2)'), then: 1.0);
    await f.settle();
    await t.ensureVisible(find.text('Make the group'));
    await t.pump();
    await f.hold(1.4);
    await f.tap(find.text('Make the group'), then: 1.0);
    await f.settle();

    f.say('Taskiem says how each add went: one added, one invited.');
    await f.hold(4.0);
    f.say('He taps Done, and the group opens.');
    await f.tap(find.text('Done'), then: 1.0);
    await f.settle();
    await f.hold(2.2);

    f.say('He taps the group’s picture. Its info shows the name and purpose.');
    await f.tap(find.descendant(of: find.byType(ChatHeadBar), matching: find.byType(PersonFace)), then: 1.0);
    await f.settle();
    await f.hold(5.0);
    await f.tap(find.bySemanticsLabel(RegExp('^Back')).first, then: 1.0);
    await f.settle();
    await f.hold(0.6);
    f.say('In the group, the tools button offers Order together.');
    await f.tap(find.bySemanticsLabel('Chat tools'), then: 1.0);
    await f.settle();
    await f.hold(3.4);
    await f.finish();
  }, skip: !filming);
}
