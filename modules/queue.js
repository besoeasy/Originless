// Queue and state management

// CID queues for pinning
let selfCidQueue = [];
let friendsCidQueue = [];

// Counters for tracking actual pins/caches
let totalPinnedSelf = 0;
let totalCachedFriends = 0;
let lastPinnerActivity = null;

let lastNostrRun = {
  at: null,
  self: null,
  friends: null,
  error: null,
};

// Queue getters
const getSelfQueue = () => selfCidQueue;
const getFriendsQueue = () => friendsCidQueue;

// Queue setters
const addToSelfQueue = (items) => {
  selfCidQueue.push(...items);
};

const addToFriendsQueue = (items) => {
  friendsCidQueue.push(...items);
};

const removeFromSelfQueue = (index) => {
  selfCidQueue.splice(index, 1);
};

const removeFromFriendsQueue = (index) => {
  friendsCidQueue.splice(index, 1);
};

// Counter getters
const getTotalPinnedSelf = () => totalPinnedSelf;
const getTotalCachedFriends = () => totalCachedFriends;
const getLastPinnerActivity = () => lastPinnerActivity;
const getLastNostrRun = () => lastNostrRun;

// Counter setters
const incrementPinnedSelf = () => {
  totalPinnedSelf++;
};

const incrementCachedFriends = () => {
  totalCachedFriends++;
};

const setLastPinnerActivity = (timestamp) => {
  lastPinnerActivity = timestamp;
};

const setLastNostrRun = (data) => {
  lastNostrRun = data;
};

module.exports = {
  getSelfQueue,
  getFriendsQueue,
  addToSelfQueue,
  addToFriendsQueue,
  removeFromSelfQueue,
  removeFromFriendsQueue,
  getTotalPinnedSelf,
  getTotalCachedFriends,
  getLastPinnerActivity,
  getLastNostrRun,
  incrementPinnedSelf,
  incrementCachedFriends,
  setLastPinnerActivity,
  setLastNostrRun,
};
