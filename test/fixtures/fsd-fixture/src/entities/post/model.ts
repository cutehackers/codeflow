export interface Post {
  id: string;
  title: string;
  content: string;
  likes: number;
}

export interface Comment {
  id: string;
  postId: string;
  authorId: string;
  text: string;
  createdAt: number;
}
